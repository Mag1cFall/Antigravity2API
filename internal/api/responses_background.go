package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// backgroundRun 是进行中后台响应的取消函数与生成结束信号
type backgroundRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// respondBackground 保存并返回 queued 状态的响应对象，随后在本请求内完成生成：响应带 Content-Length 与 Connection: close 使客户端立即收到完整响应，生成开始后保存为 in_progress，结束后保存最终对象，被 cancel 接口终止时保存为 cancelled
func (s *server) respondBackground(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string) {
	ctx, cancel := context.WithCancel(r.Context())
	run := backgroundRun{cancel: cancel, done: make(chan struct{})}
	s.background.Store(id, run)
	defer func() {
		s.background.Delete(id)
		cancel()
		close(run.done)
	}()
	created := time.Now().Unix()
	queued := responseShell(id, created, "queued", request)
	s.storeResponse(id, request, prepared, generationResult{}, queued)
	body, _ := json.Marshal(queued)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Connection", "close")
	_, _ = w.Write(body)
	_ = http.NewResponseController(w).Flush()

	compaction, err := s.compactOverThreshold(r.WithContext(ctx), &request, &prepared, id)
	var events <-chan antigravity.Event
	if err == nil {
		events, err = s.generateResponses(ctx, request, &prepared)
	}
	var result generationResult
	if err == nil {
		s.storeResponse(id, request, prepared, generationResult{}, responseShell(id, created, "in_progress", request))
		result, err = consumeEvents(ctx, s.hostedImages(ctx, request.Tools, prepared.generate.Contents, events), nil)
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		requestdb.TraceFrom(ctx).Cancel(requestdb.ErrResponseCancelled)
		s.storeResponse(id, request, prepared, generationResult{}, responseShell(id, created, "cancelled", request))
	case err != nil:
		failed := responseShell(id, created, "failed", request)
		failed["error"] = map[string]any{"code": responsesErrorCode(err), "message": err.Error()}
		s.storeResponse(id, request, prepared, generationResult{}, failed)
	default:
		s.storeResponse(id, request, prepared, result, buildResponsesObject(id, created, request, result, compaction, searchOnly(prepared.generate.Tools)))
	}
}

// streamBackground 以后台模式发送流式响应：事件依次为 queued 状态的 response.created 与 response.queued、生成开始后的 response.in_progress、输出项与终止事件，都写入事件记录；本请求从头跟随事件记录，客户端断开后生成继续，GET ?stream=true&starting_after= 从指定序号之后续传；被 cancel 接口终止时保存为 cancelled 并结束流，其他错误（含超时）写出 response.failed
func (s *server) streamBackground(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string) {
	ctx, cancel := context.WithCancel(r.Context())
	run := backgroundRun{cancel: cancel, done: make(chan struct{})}
	s.background.Store(id, run)
	stream := newStreamLog()
	prepared.stream = stream
	following := make(chan struct{})
	go func() {
		defer close(following)
		if streamHeaders(w) == nil {
			_ = stream.follow(context.WithoutCancel(r.Context()), w, -1)
		}
	}()
	defer func() {
		stream.finish()
		<-following
		s.background.Delete(id)
		cancel()
		close(run.done)
	}()
	created := time.Now().Unix()
	queued := responseShell(id, created, "queued", request)
	s.storeResponse(id, request, prepared, generationResult{}, queued)
	writer := &responsesStreamWriter{w: stream, id: id, created: created, request: request, output: &responsesOutput{id: id, request: request}}
	_ = writer.emit("response.created", map[string]any{"response": queued})
	_ = writer.emit("response.queued", map[string]any{"response": queued})

	compaction, err := s.compactOverThreshold(r.WithContext(ctx), &request, &prepared, id)
	prepared.stream = stream
	var events <-chan antigravity.Event
	if err == nil {
		events, err = s.generateResponses(ctx, request, &prepared)
	}
	reported := func(error) bool { return !errors.Is(ctx.Err(), context.Canceled) }
	if err == nil {
		events = s.hostedImages(ctx, request.Tools, prepared.generate.Contents, events)
		inProgress := responseShell(id, created, "in_progress", request)
		s.storeResponse(id, request, prepared, generationResult{}, inProgress)
		_ = writer.emit("response.in_progress", map[string]any{"response": inProgress})
		err = s.streamOutput(ctx, writer, prepared, events, compaction, reported)
	} else if reported(err) {
		_ = writer.failed(err)
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		requestdb.TraceFrom(ctx).Cancel(requestdb.ErrResponseCancelled)
		s.storeResponse(id, request, prepared, generationResult{}, responseShell(id, created, "cancelled", request))
	case err != nil:
		failed := responseShell(id, created, "failed", request)
		failed["error"] = map[string]any{"code": responsesErrorCode(err), "message": err.Error()}
		s.storeResponse(id, request, prepared, generationResult{}, failed)
	}
}

// streamLog 是后台流式响应写出的事件帧，下标即 sequence_number；作为 http.ResponseWriter 接收写出器的帧，心跳注释帧不记录
type streamLog struct {
	header http.Header
	mu     sync.Mutex
	frames [][]byte
	done   bool
	wake   chan struct{}
}

// newStreamLog 返回空的事件记录
func newStreamLog() *streamLog {
	return &streamLog{header: http.Header{}, wake: make(chan struct{})}
}

// Header 返回不发送的响应头
func (l *streamLog) Header() http.Header { return l.header }

// WriteHeader 忽略状态码
func (l *streamLog) WriteHeader(int) {}

// Flush 满足写出器的刷新调用
func (l *streamLog) Flush() {}

// Write 记录一个事件帧并唤醒跟随者
func (l *streamLog) Write(frame []byte) (int, error) {
	if bytes.HasPrefix(frame, []byte(":")) {
		return len(frame), nil
	}
	l.mu.Lock()
	l.frames = append(l.frames, bytes.Clone(frame))
	close(l.wake)
	l.wake = make(chan struct{})
	l.mu.Unlock()
	return len(frame), nil
}

// finish 标记事件已全部写出并唤醒跟随者
func (l *streamLog) finish() {
	l.mu.Lock()
	if !l.done {
		l.done = true
		close(l.wake)
		l.wake = make(chan struct{})
	}
	l.mu.Unlock()
}

// follow 把序号 after 之后的事件帧写给客户端并跟随到事件写完、客户端断开或写出失败，等待新事件时按心跳间隔写注释帧
func (l *streamLog) follow(ctx context.Context, w http.ResponseWriter, after int) error {
	next := max(after+1, 0)
	heartbeat := time.NewTimer(streamHeartbeatInterval)
	defer heartbeat.Stop()
	for {
		l.mu.Lock()
		frames, done, wake := l.frames[min(next, len(l.frames)):], l.done, l.wake
		l.mu.Unlock()
		for _, frame := range frames {
			if _, err := w.Write(frame); err != nil {
				return err
			}
			next++
		}
		if len(frames) > 0 {
			if err := http.NewResponseController(w).Flush(); err != nil {
				return err
			}
		}
		if done {
			return nil
		}
		heartbeat.Reset(streamHeartbeatInterval)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		case <-heartbeat.C:
			if err := writeSSEHeartbeat(w); err != nil {
				return err
			}
		}
	}
}
