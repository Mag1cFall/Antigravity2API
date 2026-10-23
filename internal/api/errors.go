package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

type httpStatusError interface {
	HTTPStatus() int
}

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
	if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeGeminiHeartbeat 发送不构成事件的空行并传播连接错误
func writeGeminiHeartbeat(w http.ResponseWriter) error {
	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}
	return http.NewResponseController(w).Flush()
}

// writeAnthropicPing 发送 Anthropic 流的 ping 事件
func writeAnthropicPing(w http.ResponseWriter) error {
	return writeSSE(w, "ping", map[string]string{"type": "ping"})
}

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
	return err != nil && (!errors.Is(err, context.Canceled) || r.Context().Err() == nil)
}

func validHTTPStatus(status int) int {
	if status >= 400 && status <= 599 {
		return status
	}
	return http.StatusBadGateway
}

func writeAuthError(w http.ResponseWriter, r *http.Request) {
	switch protocolForRequest(r) {
	case "anthropic":
		writeAnthropicError(w, http.StatusUnauthorized, "authentication_error", "Invalid API Key")
	case "gemini":
		writeGeminiError(w, http.StatusUnauthorized, "UNAUTHENTICATED", "Invalid API Key")
	default:
		writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "Invalid API Key")
	}
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

func protocolForRequest(r *http.Request) string {
	switch {
	case strings.HasPrefix(r.URL.Path, "/v1beta/"), strings.HasPrefix(r.URL.Path, "/upload/"), strings.HasPrefix(r.URL.Path, "/v1/interactions"):
		return "gemini"
	case strings.HasPrefix(r.URL.Path, "/v1/messages"), r.Header.Get("Anthropic-Version") != "":
		return "anthropic"
	default:
		return "openai"
	}
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

// publicErrorMessage 在 5xx 错误原因前加官方英文开头
func publicErrorMessage(status int, message string) string {
	if prefix := serverErrorPrefix(status); prefix != "" && !strings.HasPrefix(message, prefix) {
		return prefix + ": " + message
	}
	return message
}

func writeOpenAIError(w http.ResponseWriter, status int, code string, message string) {
	message = publicErrorMessage(status, message)
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"message": message,
		"type":    openAIErrorType(status, code),
		"param":   nil,
		"code":    code,
	}})
}

func openAIErrorType(status int, code string) string {
	if status == http.StatusBadRequest || status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound || code == "invalid_request" || code == "invalid_api_key" || code == "model_not_found" {
		return "invalid_request_error"
	}
	if code == "rate_limit_exceeded" {
		return "requests"
	}
	return "server_error"
}

// writeOpenAIRequestError 以 OpenAI 错误体写出请求失败，可重试时带 Retry-After
func writeOpenAIRequestError(w http.ResponseWriter, err error) {
	setRetryAfter(w, err)
	writeOpenAIError(w, statusFromError(err), openAIErrorCode(err), err.Error())
}

// writeAnthropicRequestError 以 Anthropic 错误体写出请求失败，可重试时带 Retry-After
func writeAnthropicRequestError(w http.ResponseWriter, err error) {
	setRetryAfter(w, err)
	writeAnthropicError(w, statusFromError(err), anthropicErrorType(err), anthropicErrorMessage(err))
}

// writeGeminiRequestError 以 Gemini 错误体写出请求失败，可重试时带 Retry-After 与 RetryInfo
func writeGeminiRequestError(w http.ResponseWriter, err error) {
	status := statusFromError(err)
	payload := map[string]any{"code": status, "message": err.Error(), "status": geminiErrorStatus(err)}
	if seconds := setRetryAfter(w, err); seconds > 0 {
		payload["details"] = []any{map[string]any{"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": strconv.FormatInt(seconds, 10) + "s"}}
	}
	writeJSON(w, status, map[string]any{"error": payload})
}

// setRetryAfter 在错误给出可重试时刻时写入 Retry-After 秒数并返回它，否则返回 0
func setRetryAfter(w http.ResponseWriter, err error) int64 {
	var retry interface{ RetryAt(time.Time) time.Time }
	if !errors.As(err, &retry) {
		return 0
	}
	now := time.Now()
	at := retry.RetryAt(now)
	if !at.After(now) {
		return 0
	}
	seconds := int64(math.Ceil(at.Sub(now).Seconds()))
	w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	return seconds
}

func writeAnthropicError(w http.ResponseWriter, status int, errorType string, message string) {
	message = publicErrorMessage(status, message)
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errorType,
			"message": message,
		},
	})
}

func writeGeminiError(w http.ResponseWriter, status int, statusName string, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{
		"code":    status,
		"message": message,
		"status":  statusName,
	}})
}

func openAIErrorCode(err error) string {
	if errors.Is(err, context.Canceled) {
		return "request_canceled"
	}
	if contextOverflow(err) {
		return "context_length_exceeded"
	}
	var blocked *antigravity.PromptBlockedError
	if errors.As(err, &blocked) {
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

// anthropicErrorMessage 返回 Anthropic 错误消息，上下文超限为 prompt is too long 开头的上游原因
func anthropicErrorMessage(err error) string {
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) && contextOverflow(err) {
		_, text := upstream.Message()
		if !strings.HasPrefix(text, "prompt is too long") {
			text = "prompt is too long: " + text
		}
		return text
	}
	return err.Error()
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
