package requestdb

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// 请求结果的取值
const (
	StateRunning   = "running"
	StateCompleted = "completed"
	StateToolCalls = "tool_calls"
	StateLimited   = "limited"
	StateBlocked   = "blocked"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// StatusClientClosed 是请求被取消时记录的状态码
const StatusClientClosed = 499

// ErrRequestTimeout 是请求超过 REQUEST_TIMEOUT 时的取消原因，公开协议以 504 返回
var ErrRequestTimeout error = requestTimeout{}

// requestTimeout 是 ErrRequestTimeout 的类型
type requestTimeout struct{}

func (requestTimeout) Error() string {
	return "请求超过 REQUEST_TIMEOUT"
}

// HTTPStatus 返回公开协议使用的状态码
func (requestTimeout) HTTPStatus() int {
	return http.StatusGatewayTimeout
}

// ErrCancelledByAdmin 是管理端停止请求时的取消原因
var ErrCancelledByAdmin = errors.New("管理端停止了请求")

// ErrResponseCancelled 是客户端经 cancel 接口终止后台响应时的取消原因
var ErrResponseCancelled = errors.New("客户端取消了后台响应")

// traceKey 是 Trace 在 context 中的键
type traceKey struct{}

// Trace 收集一次公开请求在各层产生的记录字段，除 Snapshot 与 Finish 外的方法都可在 nil 上调用
type Trace struct {
	mu      sync.Mutex
	started time.Time
	begun   bool
	done    bool
	row     Row
	err     error
	finish  string
	pending *antigravity.Usage
	// cancelled 是请求 context 之外取消生成的原因
	cancelled error
}

// NewTrace 以请求进入服务的时间与请求方字段开始一条记录
func NewTrace(started time.Time, row Row) *Trace {
	return &Trace{started: started, row: row}
}

// WithTrace 返回携带 trace 的 context
func WithTrace(ctx context.Context, trace *Trace) context.Context {
	return context.WithValue(ctx, traceKey{}, trace)
}

// TraceFrom 返回 context 中的 Trace，不存在时返回 nil
func TraceFrom(ctx context.Context) *Trace {
	trace, _ := ctx.Value(traceKey{}).(*Trace)
	return trace
}

// Begin 记录公开协议响应 ID 与公开模型并返回是否为首次生成，同一公开请求多次生成时保留首次的值
func (t *Trace) Begin(responseID, model string) bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.begun {
		return false
	}
	t.begun, t.row.ResponseID = true, responseID
	if model != "" {
		t.row.Model = model
	}
	return true
}

// SetRequest 记录客户端是否请求流式与请求体中的模型，生成请求的模型由 Begin 覆盖
func (t *Trace) SetRequest(stream bool, model string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.row.Stream = stream
	if t.row.Model == "" {
		t.row.Model = model
	}
}

// SetAccount 记录执行账户并写进度日志，首次调用时记下从请求进入到取得账户的排队耗时
func (t *Trace) SetAccount(account string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	previous, id := t.row.Account, t.row.ID
	if previous == "" {
		t.row.QueueMS = time.Since(t.started).Milliseconds()
	}
	t.row.Account = account
	queue := t.row.QueueMS
	t.mu.Unlock()
	switch {
	case previous == "":
		progress(slog.LevelDebug, id, account, fmt.Sprintf("分配账户 | 账户=%s | 排队=%dms", account, queue))
	case previous != account:
		progress(slog.LevelWarn, id, account, fmt.Sprintf("切换账户 | 账户=%s | 上一账户=%s", account, previous))
	}
}

// SetUpstream 记录发往上游的模型 ID、额度组与信封 requestType
func (t *Trace) SetUpstream(model, quotaGroup, requestType string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.row.UpstreamModel, t.row.QuotaGroup, t.row.RequestType = model, quotaGroup, requestType
}

// SetConversation 记录上游会话的 cascade_id 与步序号
func (t *Trace) SetConversation(cascadeID string, step int) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.row.CascadeID, t.row.StepIndex = cascadeID, step
}

// AddAttempt 追加一次上游尝试并写进度日志；错误响应中的错误码、重试延迟与说明在调用方未填写时从响应体读取
func (t *Trace) AddAttempt(attempt Attempt) {
	if t == nil {
		return
	}
	if attempt.Status >= http.StatusBadRequest {
		code, delay, message := upstreamError(attempt.Response)
		attempt.Code = cmp.Or(attempt.Code, code)
		attempt.RetryDelay = cmp.Or(attempt.RetryDelay, delay)
		attempt.Error = cmp.Or(attempt.Error, message)
	}
	t.mu.Lock()
	t.row.Attempts = append(t.row.Attempts, attempt)
	id, index := t.row.ID, len(t.row.Attempts)
	t.mu.Unlock()
	if attempt.Status == http.StatusOK && attempt.Error == "" {
		progress(slog.LevelDebug, id, attempt.Account, fmt.Sprintf("上游尝试完成 | 第 %d 次 | 状态=200 | traceId=%s | 耗时=%dms",
			index, attempt.TraceID, attempt.DurationMS))
		return
	}
	progress(slog.LevelWarn, id, attempt.Account, fmt.Sprintf("上游尝试失败 | 第 %d 次 | 状态=%d | 错误码=%s | 重试延迟=%s | 耗时=%dms | 错误=%s",
		index, attempt.Status, attempt.Code, attempt.RetryDelay, attempt.DurationMS, attempt.Error))
}

// Observe 按生成事件累计首事件耗时、用量、工具调用、终止原因、上游模型版本与流中错误；其他事件携带的累计用量暂存，流在 usage 事件之前结束时由 Finish 计入
func (t *Trace) Observe(event antigravity.Event) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if event.Usage != nil && event.Kind != antigravity.EventUsage {
		t.pending = event.Usage
	}
	switch event.Kind {
	case antigravity.EventText, antigravity.EventReasoning, antigravity.EventToolCall:
		if t.row.FirstEventMS == 0 {
			t.row.FirstEventMS = max(time.Since(t.started).Milliseconds(), 1)
		}
		if event.Kind == antigravity.EventToolCall {
			t.row.ToolCalls++
		}
	case antigravity.EventUsage:
		t.addUsage(event.Usage)
		t.pending = nil
	case antigravity.EventFinish:
		t.done, t.finish = true, event.FinishReason
		if event.ProviderModel != "" {
			t.row.ModelVersion = event.ProviderModel
		}
	case antigravity.EventError:
		t.err = event.Err
	}
}

// addUsage 把一次生成的用量累加到记录
func (t *Trace) addUsage(usage *antigravity.Usage) {
	if usage == nil {
		return
	}
	t.row.InputTokens += usage.InputTokens
	t.row.CachedTokens += usage.CachedTokens
	t.row.ReasoningTokens += usage.ReasoningTokens
	t.row.ReplyTokens += usage.OutputTokens
}

// Fail 记录同一请求最近一次生成未能开始的错误，生成开始时为 nil
func (t *Trace) Fail(err error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.err = err
}

// Cancel 记下请求 context 之外取消生成的原因，Finish 把它当作请求的取消原因
func (t *Trace) Cancel(cause error) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cancelled = cause
}

// Snapshot 返回进行中的记录副本
func (t *Trace) Snapshot() Row {
	t.mu.Lock()
	defer t.mu.Unlock()
	row := t.row
	row.Attempts = append([]Attempt(nil), row.Attempts...)
	row.Time = t.started
	row.State = StateRunning
	row.DurationMS = time.Since(t.started).Milliseconds()
	return row
}

// Finish 按响应状态码、请求 context 的取消原因与生成结果判定请求结果；上游已发出终止事件时取消不改变结果，message 是响应体中的错误说明，capture 为真时成功请求也保存上游尝试正文
func (t *Trace) Finish(at time.Time, status int, cause error, message string, capture bool) Entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.addUsage(t.pending)
	t.pending = nil
	row := t.row
	row.Time, row.DurationMS, row.Status = at, at.Sub(t.started).Milliseconds(), status
	if t.err != nil {
		message = t.err.Error()
	}
	if cause == nil {
		cause = t.cancelled
	}
	switch reason := strings.ToUpper(t.finish); {
	case errors.Is(cause, ErrRequestTimeout) && !t.done:
		row.State, row.Status, row.ErrorPhase, message = StateFailed, http.StatusGatewayTimeout, "timeout", cause.Error()
	case cause != nil && !t.done || status == StatusClientClosed || errors.Is(t.err, context.Canceled):
		row.State, row.Status = StateCancelled, StatusClientClosed
		message = "客户端断开连接"
		if errors.Is(cause, ErrCancelledByAdmin) || errors.Is(cause, ErrResponseCancelled) {
			message = cause.Error()
		}
	case status >= http.StatusBadRequest || t.err != nil:
		row.State = StateFailed
		if status < http.StatusBadRequest {
			row.Status = errorStatus(t.err)
		}
		row.ErrorPhase = t.phase(row.Status)
	case reason == "MAX_TOKENS":
		row.State = StateLimited
	case reason != "" && reason != "STOP":
		row.State, message = StateBlocked, t.finish
	case row.ToolCalls > 0:
		row.State = StateToolCalls
	default:
		row.State = StateCompleted
	}
	if row.State == StateFailed && message == "" {
		message = http.StatusText(row.Status)
	}
	if row.State != StateCompleted && row.State != StateToolCalls {
		row.Error = message
	}
	if count := len(row.Attempts); count != 0 {
		last := row.Attempts[count-1]
		row.UpstreamStatus, row.ErrorCode = last.Status, last.Code
	}
	entry := Entry{Row: row}
	if capture || row.State == StateFailed {
		for _, attempt := range row.Attempts {
			entry.Upstream = append(entry.Upstream, NewBody(attempt.Request, attempt.Response))
		}
	}
	return entry
}

// phase 返回失败请求的错误阶段：未调用生成为 request，没有上游尝试为 routing，最后一次尝试连接失败为 network
func (t *Trace) phase(status int) string {
	count := len(t.row.Attempts)
	switch {
	case !t.begun && status < http.StatusInternalServerError:
		return "request"
	case !t.begun:
		return "internal"
	case count == 0:
		return "routing"
	case t.row.Attempts[count-1].Status == 0:
		return "network"
	}
	return "upstream"
}

// errorStatus 返回响应头已发出后流中错误对应的状态码，取错误自带的 HTTP 状态或上游状态，否则为 502
func errorStatus(err error) int {
	var coded interface{ HTTPStatus() int }
	if errors.As(err, &coded) && coded.HTTPStatus() >= http.StatusBadRequest {
		return coded.HTTPStatus()
	}
	var upstream *antigravity.UpstreamError
	if errors.As(err, &upstream) && upstream.Status >= http.StatusBadRequest {
		return upstream.Status
	}
	return http.StatusBadGateway
}

// upstreamError 读取 Google 错误体的错误码、重试延迟与说明；错误码取 ErrorInfo.reason，没有时取 error.status，重试延迟取 RetryInfo.retryDelay，没有时取 ErrorInfo.metadata.quotaResetDelay
func upstreamError(body []byte) (string, string, string) {
	data := bytes.TrimSpace(body)
	if bytes.HasPrefix(data, []byte("[")) {
		var list []json.RawMessage
		if json.Unmarshal(data, &list) == nil && len(list) != 0 {
			data = list[0]
		}
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Reason     string `json:"reason"`
				RetryDelay string `json:"retryDelay"`
				Metadata   struct {
					QuotaResetDelay string `json:"quotaResetDelay"`
				} `json:"metadata"`
			} `json:"details"`
		} `json:"error"`
	}
	json.Unmarshal(data, &payload)
	code, delay, reset := payload.Error.Status, "", ""
	for _, detail := range payload.Error.Details {
		code = cmp.Or(detail.Reason, code)
		delay = cmp.Or(detail.RetryDelay, delay)
		reset = cmp.Or(detail.Metadata.QuotaResetDelay, reset)
	}
	return code, cmp.Or(delay, reset), payload.Error.Message
}

// progress 写一条 request.progress 日志
func progress(level slog.Level, id, account, message string) {
	slog.Log(context.Background(), level, message, "source", account, "event", "request.progress", "request_id", id)
}
