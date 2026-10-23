package app

import (
	"bytes"
	"cmp"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/logging"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// stallThreshold 是生成事件流无进展多久后写停顿诊断
const stallThreshold = 15 * time.Second

// activeKey 是 activeRequest 在 context 中的键
type activeKey struct{}

// recordingService 在公开协议调用生成时把响应 ID、公开模型与生成事件写入请求的 Trace
type recordingService struct {
	antigravity.Service
	server *server
}

// Generate 记录首次生成，记下本次生成未能开始的错误或清除先前的错误，并经 relay 转发事件
func (service recordingService) Generate(ctx context.Context, request antigravity.GenerateRequest) (<-chan antigravity.Event, error) {
	trace := requestdb.TraceFrom(ctx)
	if trace.Begin(request.ID, request.Model) {
		row := trace.Snapshot()
		service.server.events.begin(row)
		slog.InfoContext(ctx, fmt.Sprintf("请求开始 | 模型=%s | 协议=%s | 流式=%t | 档位=%s | %s",
			row.Model, row.Protocol, row.Stream, row.PromptTier, parameters(request.Config)),
			"event", "request.started", "source", "request", "request", row)
	}
	events, err := service.Service.Generate(ctx, request)
	trace.Fail(err)
	if err != nil {
		return nil, err
	}
	active := ctx.Value(activeKey{}).(*activeRequest)
	observed := make(chan antigravity.Event)
	active.relays.Go(func() {
		defer close(observed)
		relay(ctx, trace, request.Model, events, observed)
	})
	return observed, nil
}

// relay 记录并转发生成事件，15 秒无事件时写停顿诊断，恢复后写恢复日志；客户端断开后继续排空上游事件以记下已知用量
func relay(ctx context.Context, trace *requestdb.Trace, model string, events <-chan antigravity.Event, observed chan<- antigravity.Event) {
	started := time.Now()
	timer := time.NewTimer(stallThreshold)
	defer timer.Stop()
	first, forward, last := true, true, "-"
	var stalled time.Time
	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			trace.Observe(event)
			now := time.Now()
			if !stalled.IsZero() {
				progress(ctx, trace, slog.LevelInfo, fmt.Sprintf("事件流恢复 | 模型=%s | 停顿=%s | 当前事件=%s", model, now.Sub(stalled).Round(time.Millisecond)+stallThreshold, event.Kind))
				stalled = time.Time{}
			} else if first {
				progress(ctx, trace, slog.LevelDebug, fmt.Sprintf("上游首事件到达 | 模型=%s | 等待=%s | 事件=%s", model, now.Sub(started).Round(time.Millisecond), event.Kind))
			}
			first, last = false, string(event.Kind)
			timer.Reset(stallThreshold)
			if forward {
				select {
				case observed <- event:
				case <-ctx.Done():
					forward = false
				}
			}
		case <-timer.C:
			if ctx.Err() != nil {
				continue
			}
			stalled = time.Now()
			message := fmt.Sprintf("事件流停顿 | 模型=%s | 已等待=%s | 最近事件=%s", model, stallThreshold, last)
			if first {
				message = fmt.Sprintf("上游首事件等待 | 模型=%s | 已等待=%s", model, stallThreshold)
			}
			progress(ctx, trace, slog.LevelWarn, message)
		}
	}
}

// progress 写一条以请求账户为来源的 request.progress 日志
func progress(ctx context.Context, trace *requestdb.Trace, level slog.Level, message string) {
	slog.Log(ctx, level, message, "event", "request.progress", "source", cmp.Or(trace.Snapshot().Account, "request"))
}

// parameters 返回请求的采样与思考参数文本，未设置的项写 -
func parameters(config antigravity.GenerationConfig) string {
	text := func(value *float64) string {
		if value == nil {
			return "-"
		}
		return strconv.FormatFloat(*value, 'f', -1, 64)
	}
	integer := func(value *int64) string {
		if value == nil {
			return "-"
		}
		return strconv.FormatInt(*value, 10)
	}
	return fmt.Sprintf("温度=%s | TopP=%s | 思考=%s | 思考预算=%s | 最大输出=%s",
		text(config.Temperature), text(config.TopP), cmp.Or(config.ReasoningEffort, "-"), integer(config.ThinkingBudget), integer(config.MaxOutputTokens))
}

// recordPublic 为通过 API key 校验的 POST 请求建立 Trace 与停止入口，结束后写入请求账本并推送请求事件与日志；Responses 后台请求的 context 不随客户端连接关闭而取消
func (s *server) recordPublic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg := s.live.Load()
		key, authorized := apiKey(r, cfg.APIKeys)
		if r.Method != http.MethodPost || !authorized {
			next.ServeHTTP(w, r)
			return
		}
		started := time.Now()
		id := logging.RequestID(r.Context())
		trace := requestdb.NewTrace(started, requestdb.Row{
			ID: id, ClientRequestID: r.Header.Get("X-Request-ID"), Protocol: protocolOf(r.URL.Path), Path: r.URL.Path,
			APIKey: key, ClientIP: clientIP(r), UserAgent: r.UserAgent(), PromptTier: cfg.SystemPrompt,
		})
		trace.Watch(s.events.begin)
		body, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(body))
		trace.SetRequest(probe(r.URL.Path, body))
		base := r.Context()
		if background(r.URL.Path, body) {
			base = context.WithoutCancel(base)
		}
		ctx, cancel := context.WithCancelCause(base)
		defer cancel(nil)
		active := &activeRequest{trace: trace, cancel: cancel}
		s.events.track(id, active)
		capture := s.ledger.BodyCapture()
		writer := &recordWriter{ResponseWriter: w, keep: capture}
		ctx = requestdb.WithTrace(context.WithValue(ctx, activeKey{}, active), trace)
		next.ServeHTTP(writer, r.WithContext(ctx))
		active.relays.Wait()
		status := cmp.Or(writer.status, http.StatusOK)
		var cause error
		if ctx.Err() != nil {
			cause = context.Cause(ctx)
		}
		entry := trace.Finish(time.Now(), status, cause, errorMessage(writer.data, status), capture)
		if capture {
			entry.Body = &requestdb.Body{
				Request: string(body[:min(len(body), requestdb.BodyLimit)]), RequestSize: int64(len(body)),
				Response: string(writer.data), ResponseSize: writer.size,
			}
		}
		s.ledger.Record(entry)
		s.events.end(id, entry.Row)
		row := entry.Row
		level, message := outcome(row.State)
		text := fmt.Sprintf("%s | 状态=%d | 耗时=%s | 账户=%s | 模型=%s | 上游模型=%s | 输入=%d | 缓存=%d | 思考=%d | 回复=%d",
			message, row.Status, (time.Duration(row.DurationMS) * time.Millisecond).String(), cmp.Or(row.Account, "-"), row.Model,
			cmp.Or(row.UpstreamModel, "-"), row.InputTokens, row.CachedTokens, row.ReasoningTokens, row.ReplyTokens)
		if row.Error != "" {
			text += " | 错误=" + row.Error
		}
		slog.Log(r.Context(), level, text, "event", "request.finished", "source", cmp.Or(row.Account, "request"), "request", row)
	})
}

// apiKey 返回请求所带 API key 的掩码与是否通过校验，keys 为空时全部通过且掩码为空
func apiKey(r *http.Request, keys []string) (string, bool) {
	if len(keys) == 0 {
		return "", true
	}
	candidates := []string{r.Header.Get("X-Goog-API-Key"), r.Header.Get("X-API-Key"), r.URL.Query().Get("key")}
	if scheme, value, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " "); ok && strings.EqualFold(scheme, "Bearer") {
		candidates = append(candidates, value)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		for _, key := range keys {
			if candidate != "" && subtle.ConstantTimeCompare([]byte(candidate), []byte(key)) == 1 {
				return maskKey(key), true
			}
		}
	}
	return "", false
}

// maskKey 保留 key 的前后各不超过四分之一，前缀最多 3 个字符、后缀最多 4 个字符
func maskKey(key string) string {
	head, tail := min(3, len(key)/4), min(4, len(key)/4)
	return key[:head] + "…" + key[len(key)-tail:]
}

// protocolOf 按公开 API 路径归类协议
func protocolOf(path string) string {
	switch {
	case path == "/v1/chat/completions":
		return "openai-chat"
	case strings.HasPrefix(path, "/v1/responses"):
		return "openai-responses"
	case strings.HasPrefix(path, "/v1/messages"):
		return "anthropic"
	case strings.HasPrefix(path, "/v1beta/"):
		return "gemini"
	}
	return "other"
}

// probe 从路径或请求体读取客户端是否请求流式与请求的模型
func probe(path string, body []byte) (bool, string) {
	if rest, ok := strings.CutPrefix(path, "/v1beta/models/"); ok {
		model, action, _ := strings.Cut(rest, ":")
		return action == "streamGenerateContent", model
	}
	var request struct {
		Stream bool   `json:"stream"`
		Model  string `json:"model"`
	}
	json.Unmarshal(body, &request)
	return request.Stream, request.Model
}

// background 返回请求是否为 Responses 后台请求，后台请求的生成不随客户端断开而取消
func background(path string, body []byte) bool {
	var request struct {
		Background bool `json:"background"`
	}
	json.Unmarshal(body, &request)
	return path == "/v1/responses" && request.Background
}

// errorMessage 返回错误响应体中 error.message 的文本，四个公开协议的错误体都有该字段
func errorMessage(body []byte, status int) string {
	if status < http.StatusBadRequest {
		return ""
	}
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(body, &payload)
	return payload.Error.Message
}

// outcome 返回请求结果对应的日志级别与摘要
func outcome(state string) (slog.Level, string) {
	switch state {
	case requestdb.StateToolCalls:
		return slog.LevelInfo, "工具调用完成"
	case requestdb.StateLimited:
		return slog.LevelWarn, "达到输出上限"
	case requestdb.StateBlocked:
		return slog.LevelWarn, "上游终止生成"
	case requestdb.StateFailed:
		return slog.LevelError, "请求失败"
	case requestdb.StateCancelled:
		return slog.LevelWarn, "请求已取消"
	}
	return slog.LevelInfo, "请求完成"
}

// recordWriter 记录响应状态码，并在保存正文或响应为错误时复制前 64 KiB
type recordWriter struct {
	http.ResponseWriter
	status int
	keep   bool
	data   []byte
	size   int64
}

// WriteHeader 记录首个状态码
func (writer *recordWriter) WriteHeader(status int) {
	if writer.status == 0 {
		writer.status = status
	}
	writer.ResponseWriter.WriteHeader(status)
}

// Write 写出并复制响应体
func (writer *recordWriter) Write(data []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	count, err := writer.ResponseWriter.Write(data)
	writer.size += int64(count)
	if writer.keep || writer.status >= http.StatusBadRequest {
		writer.data = append(writer.data, data[:min(count, max(requestdb.BodyLimit-len(writer.data), 0))]...)
	}
	return count, err
}

// FlushError 刷新底层响应并返回错误，未写状态码时记为 200
func (writer *recordWriter) FlushError() error {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

// Unwrap 返回底层响应供 http.ResponseController 使用
func (writer *recordWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
