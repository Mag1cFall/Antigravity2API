package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/i18n"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// httpStatusError 为自带 HTTP 状态码的错误
type httpStatusError interface {
	HTTPStatus() int
}

// decodeJSON 把请求体解析为 target，请求体只能含一个 JSON 值
func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("request body must contain one JSON value")
	}
	return nil
}

// writeJSON 以指定状态码写出 JSON 响应
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// streamHeaders 写入并刷新流式响应头
func streamHeaders(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	return http.NewResponseController(w).Flush()
}

// writeSSE 写入具名事件并传播缓冲刷新错误
func writeSSE(w http.ResponseWriter, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	frame := make([]byte, 0, len(event)+len(data)+16)
	if event != "" {
		frame = append(append(append(frame, "event: "...), event...), '\n')
	}
	frame = append(append(append(frame, "data: "...), data...), "\n\n"...)
	if _, err := w.Write(frame); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeSSEText 写入文本事件并刷新网络缓冲
func writeSSEText(w http.ResponseWriter, data string) error {
	if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeSSEHeartbeat 发送 SSE 注释行心跳并传播连接错误
func writeSSEHeartbeat(w http.ResponseWriter) error {
	if _, err := io.WriteString(w, sseHeartbeat); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// statusFromError 返回错误对应的 HTTP 状态：客户端取消为 503，超时为 504，上游错误与带状态的错误取其 4xx、5xx 状态，其余为 502
func statusFromError(err error) int {
	if errors.Is(err, context.Canceled) {
		return http.StatusServiceUnavailable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout
	}
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) {
		return validHTTPStatus(upstream.Status)
	}
	var statusErr httpStatusError
	if errors.As(err, &statusErr) {
		return validHTTPStatus(statusErr.HTTPStatus())
	}
	return http.StatusBadGateway
}

// shouldWriteRequestError 判断仍在线的客户端是否需要收到结构化错误
func shouldWriteRequestError(r *http.Request, err error) bool {
	return !errors.Is(err, context.Canceled) || r.Context().Err() == nil
}

// validHTTPStatus 保留 400 至 599 的状态码，其余换成 502
func validHTTPStatus(status int) int {
	if status >= 400 && status <= 599 {
		return status
	}
	return http.StatusBadGateway
}

// writeAuthError 按请求所属协议写入 API key 错误，OpenAI 与 Anthropic 为 401
func writeAuthError(w http.ResponseWriter, r *http.Request) {
	switch protocolForRequest(r) {
	case "anthropic":
		writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid API Key")
	case "gemini":
		writeGeminiAuthError(w, r)
	default:
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	}
}

// geminiInvalidKeyMessage 是 Gemini API 对无效 API key 的错误信息
const geminiInvalidKeyMessage = "API key not valid. Please pass a valid API key."

// geminiAuthFailure 是 Gemini API 认证错误的 error 对象
type geminiAuthFailure struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
	Details []any  `json:"details,omitempty"`
}

// geminiErrorInfo 是 Gemini 错误 details 中的 google.rpc.ErrorInfo
type geminiErrorInfo struct {
	Type     string            `json:"@type"`
	Reason   string            `json:"reason"`
	Domain   string            `json:"domain"`
	Metadata map[string]string `json:"metadata"`
}

// geminiLocalizedMessage 是 Gemini 错误 details 中的 google.rpc.LocalizedMessage
type geminiLocalizedMessage struct {
	Type    string `json:"@type"`
	Locale  string `json:"locale"`
	Message string `json:"message"`
}

// writeGeminiAuthError 按 Gemini API 写入 API key 错误：给出的 key 无效时为 400 INVALID_ARGUMENT 带 ErrorInfo 与 LocalizedMessage，没有 key 时为 403 PERMISSION_DENIED；Interactions 与非 SSE 的 streamGenerateContent 外层为数组
func writeGeminiAuthError(w http.ResponseWriter, r *http.Request) {
	failure := geminiAuthFailure{
		Code: http.StatusForbidden, Status: "PERMISSION_DENIED",
		Message: "Method doesn't allow unregistered callers (callers without established identity). Please use API Key or other form of API consumer identity to call this API.",
	}
	if slices.ContainsFunc(apiKeyCandidates(r), func(key string) bool { return key != "" }) {
		failure = geminiAuthFailure{Code: http.StatusBadRequest, Message: geminiInvalidKeyMessage, Status: "INVALID_ARGUMENT", Details: []any{
			geminiErrorInfo{Type: "type.googleapis.com/google.rpc.ErrorInfo", Reason: "API_KEY_INVALID", Domain: "googleapis.com", Metadata: map[string]string{"service": "generativelanguage.googleapis.com"}},
			geminiLocalizedMessage{Type: "type.googleapis.com/google.rpc.LocalizedMessage", Locale: "en-US", Message: geminiInvalidKeyMessage},
		}}
	}
	var payload any = map[string]geminiAuthFailure{"error": failure}
	if strings.HasPrefix(r.URL.Path, "/v1/interactions") || strings.HasPrefix(r.URL.Path, "/v1beta/interactions") ||
		strings.HasSuffix(r.URL.Path, ":streamGenerateContent") && r.URL.Query().Get("alt") != "sse" {
		payload = []any{payload}
	}
	writeJSON(w, failure.Code, payload)
}

// writeRouteNotFound 以请求所属协议的错误体返回 404，消息含请求的方法与路径
func writeRouteNotFound(w http.ResponseWriter, r *http.Request) {
	message := fmt.Sprintf("Invalid URL (%s %s)", r.Method, r.URL.Path)
	switch protocolForRequest(r) {
	case "anthropic":
		writeAnthropicError(w, http.StatusNotFound, "not_found_error", message)
	case "gemini":
		writeGeminiError(w, http.StatusNotFound, "NOT_FOUND", message)
	default:
		writeOpenAIError(w, http.StatusNotFound, "not_found", message)
	}
}

// protocolForRequest 按路径与 Anthropic-Version 请求头返回请求所属协议：gemini、anthropic 或 openai
func protocolForRequest(r *http.Request) string {
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/"), strings.HasPrefix(r.URL.Path, "/upload/"), strings.HasPrefix(r.URL.Path, "/v1/interactions"), geminiV1Path(r):
		return "gemini"
	case strings.HasPrefix(r.URL.Path, "/v1/messages"), r.Header.Get("Anthropic-Version") != "":
		return "anthropic"
	default:
		return "openai"
	}
}

// geminiV1Path 表示 /v1 下的 Gemini API 请求：模型方法调用与上下文缓存，以及以 Gemini API 方式鉴权的模型与文件请求
func geminiV1Path(r *http.Request) bool {
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/v1/models/"), strings.HasPrefix(path, "/v1/cachedContents"):
		return true
	case path == "/v1/models", strings.HasPrefix(path, "/v1/models/"), path == "/v1/files", strings.HasPrefix(path, "/v1/files/"):
		return geminiAuth(r)
	}
	return false
}

// geminiAuth 表示请求以 Gemini API 的方式鉴权：带 x-goog-api-key 请求头或 key 查询参数，不带 Authorization
func geminiAuth(r *http.Request) bool {
	return r.Header.Get("Authorization") == "" && (r.Header.Get("X-Goog-Api-Key") != "" || r.URL.Query().Has("key"))
}

// serverErrorPrefix 返回 5xx 状态对应的官方英文错误开头，其余状态返回空
func serverErrorPrefix(status int) string {
	switch {
	case status == http.StatusServiceUnavailable:
		return "Service unavailable"
	case status == http.StatusGatewayTimeout:
		return "Request timed out"
	case status == 529:
		return "Overloaded"
	case status >= 500:
		return "Internal server error"
	}
	return ""
}

// emailPattern 匹配错误文案中的邮箱地址
var emailPattern = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)

// publicText 返回写给 API 客户端的错误文案：中文按英文翻译表译为英文，邮箱地址换成 [account]
func publicText(message string) string {
	return emailPattern.ReplaceAllString(i18n.Translate("en", message), "[account]")
}

// accountStates 是账户都不可调度的错误，AccountStates 返回各账户的英文状态
type accountStates interface {
	AccountStates() []string
}

// publicReason 返回请求失败写给 API 客户端的英文原因：账户都不可调度时按状态给出账户数，内容策略结束时另写拦截时间与已输出的内容，其余错误取完整原因
func publicReason(err error) string {
	var states accountStates
	var filtered *FilteredError
	switch {
	case errors.As(err, &states) && len(states.AccountStates()) > 0:
		return notReadyReason(states.AccountStates())
	case errors.As(err, &filtered):
		return filteredReason(filtered)
	}
	return publicText(err.Error())
}

// FilteredError 是上游以内容策略结束的生成，After 为请求开始到被拦截的时长，Produced 为此前已交给客户端的内容种类，隐藏的思考不计
type FilteredError struct {
	Err      error
	After    time.Duration
	Produced []string
}

func (e *FilteredError) Error() string {
	return e.Err.Error()
}

func (e *FilteredError) Unwrap() error {
	return e.Err
}

// filteredReason 返回内容策略结束的公开原因：上游原因之后写明请求开始多久后被拦截与此前已输出的内容
func filteredReason(filtered *FilteredError) string {
	produced := "none"
	if len(filtered.Produced) > 0 {
		produced = strings.Join(filtered.Produced, ", ")
	}
	return fmt.Sprintf("%s. Blocked %s after the request started; output before the block: %s", strings.TrimSuffix(publicText(filtered.Err.Error()), "."), filtered.After.Round(time.Second), produced)
}

// notReadyReason 返回账户都不可调度时的公开原因，按状态首次出现的顺序给出账户数
func notReadyReason(states []string) string {
	counts := map[string]int{}
	var order []string
	for _, state := range states {
		if counts[state] == 0 {
			order = append(order, state)
		}
		counts[state]++
	}
	parts := make([]string, len(order))
	for index, state := range order {
		parts[index] = fmt.Sprintf("%s: %d", state, counts[state])
	}
	return "No account can serve this request (accounts " + strings.Join(parts, ", ") + ")"
}

// recordFailure 把请求失败的完整原因记入请求账本，公开错误只给出英文原因
func recordFailure(r *http.Request, err error) {
	requestdb.TraceFrom(r.Context()).Fail(err)
}

// publicErrorMessage 在 5xx 错误原因前加官方英文开头
func publicErrorMessage(status int, message string) string {
	if prefix := serverErrorPrefix(status); prefix != "" && !strings.HasPrefix(message, prefix) {
		return prefix + ": " + message
	}
	return message
}

// writeOpenAIError 写出 OpenAI 错误响应
func writeOpenAIError(w http.ResponseWriter, status int, code string, message string) {
	writeJSON(w, status, openAIErrorBody(status, code, message))
}

// openAIErrorBody 返回 OpenAI 错误体，message 按 publicText 写成英文
func openAIErrorBody(status int, code string, message string) map[string]any {
	return map[string]any{"error": openAIError{
		Message: publicErrorMessage(status, publicText(message)),
		Type:    openAIErrorType(status, code),
		Code:    code,
	}}
}

// openAIError 是 OpenAI 错误体的 error 成员，字段按官方顺序排列
type openAIError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    string  `json:"code"`
}

// writeOpenAIStreamError 在流已开始后把请求错误以 OpenAI 错误体写为一个 SSE 事件，可重试时在消息末尾写明重试时间，客户端已断开时不写
func writeOpenAIStreamError(w http.ResponseWriter, r *http.Request, err error) {
	if shouldWriteRequestError(r, err) {
		_ = writeSSE(w, "", openAIErrorBody(statusFromError(err), openAIErrorCode(err), retryMessage(publicReason(err), retryAfterSeconds(err))))
	}
}

// openAIErrorType 返回状态码与错误码对应的 OpenAI 错误类型
func openAIErrorType(status int, code string) string {
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound || code == "invalid_request" || code == "invalid_api_key" || code == "model_not_found" {
		return "invalid_request_error"
	}
	if code == "rate_limit_exceeded" {
		return "requests"
	}
	return "server_error"
}

// writeOpenAIRequestError 以 OpenAI 错误体写出请求失败，可重试时带 Retry-After 并在消息末尾写明重试时间
func writeOpenAIRequestError(w http.ResponseWriter, err error) {
	seconds := setRetryAfter(w, err)
	writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), retryMessage(publicReason(err), seconds))
}

// writeAnthropicRequestError 以 Anthropic 错误体写出请求失败，可重试时带 Retry-After 并在消息末尾写明重试时间
func writeAnthropicRequestError(w http.ResponseWriter, err error) {
	seconds := setRetryAfter(w, err)
	writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), retryMessage(anthropicErrorMessage(err), seconds))
}

// retryMessage 在 seconds 大于 0 时于消息末尾按 OpenAI 限流消息的写法另起一句 Please try again in <seconds>s.
func retryMessage(message string, seconds int64) string {
	if seconds <= 0 {
		return message
	}
	if !strings.HasSuffix(message, ".") {
		message += "."
	}
	return message + " Please try again in " + strconv.FormatInt(seconds, 10) + "s."
}

// writeGeminiRequestError 以 Gemini 错误体写出请求失败，可重试时带 Retry-After 与 RetryInfo
func writeGeminiRequestError(w http.ResponseWriter, err error) {
	writeJSON(w, statusFromError(err), officialOrder("gemini", map[string]any{"error": geminiErrorPayload(err, setRetryAfter(w, err))}))
}

// geminiErrorPayload 返回 Gemini 错误对象，seconds 大于 0 时 details 带该时长的 RetryInfo
func geminiErrorPayload(err error, seconds int64) map[string]any {
	payload := map[string]any{"code": statusFromError(err), "message": publicReason(err), "status": geminiErrorStatus(err)}
	if seconds > 0 {
		payload["details"] = []any{map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": strconv.FormatInt(seconds, 10) + "s"}}
	}
	return payload
}

// setRetryAfter 在错误给出可重试时刻时写入 Retry-After 秒数并返回它，否则返回 0
func setRetryAfter(w http.ResponseWriter, err error) int64 {
	seconds := retryAfterSeconds(err)
	if seconds > 0 {
		w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	return seconds
}

// retryAfterSeconds 返回错误给出的可重试时刻距现在的秒数，没有可重试时刻时返回 0
func retryAfterSeconds(err error) int64 {
	var retry interface{ RetryAt(time.Time) time.Time }
	if !errors.As(err, &retry) {
		return 0
	}
	now := time.Now()
	at := retry.RetryAt(now)
	if !at.After(now) {
		return 0
	}
	return int64(math.Ceil(at.Sub(now).Seconds()))
}

// writeAnthropicError 写出 Anthropic 错误响应，message 按 publicText 写成英文
func writeAnthropicError(w http.ResponseWriter, status int, errorType string, message string) {
	message = publicErrorMessage(status, publicText(message))
	writeJSON(w, status, anthropicErrorResponse{Type: "error", Error: anthropicErrorBody{Type: errorType, Message: message}})
}

// anthropicErrorResponse 是 Anthropic 错误响应，字段按官方顺序排列
type anthropicErrorResponse struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

// anthropicErrorBody 是 Anthropic 错误响应的 error 成员
type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// writeGeminiError 写出 Gemini 错误响应，message 按 publicText 写成英文
func writeGeminiError(w http.ResponseWriter, status int, statusName string, message string) {
	writeJSON(w, status, officialOrder("gemini", map[string]any{"error": map[string]any{
		"code":    status,
		"message": publicText(message),
		"status":  statusName,
	}}))
}

// openAIErrorCode 返回错误对应的 OpenAI 错误码
func openAIErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if contextOverflow(err) {
		return "context_length_exceeded"
	}
	var feedback *antigravity.PromptFeedbackError
	if errors.As(err, &feedback) {
		return "invalid_prompt"
	}
	switch statusFromError(err) {
	case http.StatusNotFound:
		return "model_not_found"
	case http.StatusTooManyRequests:
		return "rate_limit_exceeded"
	}
	return "upstream_error"
}

// contextOverflow 判断错误是否为上游的上下文超限
func contextOverflow(err error) bool {
	var upstream *antigravity.UpstreamError
	if !errors.As(err, &upstream) {
		return false
	}
	overflow, _, _ := upstream.ContextOverflow()
	return overflow
}

// anthropicErrorType 返回错误的 Anthropic 类型：Claude 错误取内嵌类型，其余按 HTTP 状态
func anthropicErrorType(err error) string {
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) && upstream.AnthropicType() != "" {
		return upstream.AnthropicType()
	}
	return anthropicStatusErrorType(statusFromError(err))
}

// anthropicErrorMessage 返回 Anthropic 错误消息，上下文超限为 prompt is too long 开头的上游原因，其余按 publicReason
func anthropicErrorMessage(err error) string {
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) && contextOverflow(err) {
		_, text := upstream.Message()
		if !strings.HasPrefix(text, "prompt is too long") {
			text = "prompt is too long: " + text
		}
		return publicText(text)
	}
	return publicReason(err)
}

// anthropicStatusErrorType 返回 HTTP 状态对应的 Anthropic 错误类型
func anthropicStatusErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusGatewayTimeout:
		return "timeout_error"
	case 529:
		return "overloaded_error"
	default:
		return "api_error"
	}
}

// geminiErrorStatus 返回 Gemini 错误的 status：上游错误取其 status，其余按 HTTP 状态
func geminiErrorStatus(err error) string {
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) {
		if status, _ := upstream.Message(); status != "" {
			return status
		}
	}
	switch statusFromError(err) {
	case http.StatusBadRequest:
		return "INVALID_ARGUMENT"
	case http.StatusUnauthorized:
		return "UNAUTHENTICATED"
	case http.StatusForbidden:
		return "PERMISSION_DENIED"
	case http.StatusNotFound:
		return "NOT_FOUND"
	case http.StatusTooManyRequests:
		return "RESOURCE_EXHAUSTED"
	case http.StatusServiceUnavailable:
		return "UNAVAILABLE"
	case http.StatusGatewayTimeout:
		return "DEADLINE_EXCEEDED"
	default:
		return "INTERNAL"
	}
}
