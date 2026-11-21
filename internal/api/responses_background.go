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

// startBackground 登记一次后台运行并返回取消上下文与结束函数
func (s *server) startBackground(parent context.Context, id string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	run := backgroundRun{cancel: cancel, done: make(chan struct{})}
	s.background.Store(id, run)
	return ctx, func() {
		s.background.Delete(id)
		cancel()
		close(run.done)
	}
}

// stopBackground 终止进行中的后台响应并等待它保存结束状态
func (s *server) stopBackground(id string) {
	if value, running := s.background.Load(id); running {
		run := value.(backgroundRun)
		run.cancel()
		<-run.done
	}
}

// respondBackground 立即返回 queued 状态的非流式后台响应并在本请求内继续生成
// 响应带 Content-Length 与 Connection: close；状态流转见 docs/protocol.md「保存的响应」
func (s *server) respondBackground(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string) {
	ctx, end := s.startBackground(r.Context(), id)
	defer end()
	created := time.Now().Unix()
	queued := responseShell(id, created, "queued", request)
	s.storeResponse(id, request, prepared, generationResult{}, queued)
	body, _ := json.Marshal(officialOrder("responses", queued))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Connection", "close")
	_, _ = w.Write(body)
	_ = http.NewResponseController(w).Flush()

	compaction, err := s.compactOverThreshold(r.WithContext(ctx), &request, &prepared, id)
	prepared.generate.Background = true
	var events <-chan antigravity.Event
	if err == nil {
		events, err = s.generateResponses(ctx, request, &prepared)
	}
	var result generationResult
	if err == nil {
		s.storeResponse(id, request, prepared, generationResult{}, responseShell(id, created, "in_progress", request))
		result, err = consumeEvents(ctx, s.hostedImages(ctx, request.allTools(), prepared.generate.Session, prepared.generate.Contents, events), nil)
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		requestdb.TraceFrom(ctx).Cancel(requestdb.ErrResponseCancelled)
		s.storeResponse(id, request, prepared, generationResult{}, responseShell(id, created, "cancelled", request))
	case err != nil:
		failed := responseShell(id, created, "failed", request)
		failed["error"] = responsesError(err)
		s.storeResponse(id, request, prepared, generationResult{}, failed)
	default:
		s.storeResponse(id, request, prepared, result, buildResponsesObject(id, created, request, result, compaction, searchOnly(prepared.generate.Tools)))
	}
}

// streamBackground 以后台流式模式写入事件记录并让本请求从头跟随
// 客户端断开后生成继续；事件序列、续传与取消行为见 docs/protocol.md「保存的响应」
func (s *server) streamBackground(w http.ResponseWriter, r *http.Request, request responsesRequest, prepared responsesPreparation, id string) {
	ctx, end := s.startBackground(r.Context(), id)
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
		end()
	}()
	created := time.Now().Unix()
	queued := responseShell(id, created, "queued", request)
	s.storeResponse(id, request, prepared, generationResult{}, queued)
	writer := &responsesStreamWriter{w: stream, id: id, created: created, request: request, output: newResponsesOutput(id, request)}
	_ = writer.emit("response.created", map[string]any{"response": queued})
	_ = writer.emit("response.queued", map[string]any{"response": queued})
	stream.setPulse(writer.progress)

	compaction, err := s.compactOverThreshold(r.WithContext(ctx), &request, &prepared, id)
	prepared.stream = stream
	prepared.generate.Background = true
	var events <-chan antigravity.Event
	if err == nil {
		events, err = s.generateResponses(ctx, request, &prepared)
	}
	reported := func(error) bool { return !errors.Is(ctx.Err(), context.Canceled) }
	if err == nil {
		events = s.hostedImages(ctx, request.allTools(), prepared.generate.Session, prepared.generate.Contents, events)
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
		failed["error"] = responsesError(err)
		s.storeResponse(id, request, prepared, generationResult{}, failed)
	}
}

// streamLog 是后台流式响应写出的事件帧，下标即 sequence_number；作为 http.ResponseWriter 接收写出器的帧，心跳注释帧不记录；pulse 让写出器以新序号记录一个状态事件
type streamLog struct {
	header http.Header
	mu     sync.Mutex
	frames [][]byte
	done   bool
	wake   chan struct{}
	pulse  func() error
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

// setPulse 设置跟随者静默满心跳间隔时调用的 pulse
func (l *streamLog) setPulse(pulse func() error) {
	l.mu.Lock()
	l.pulse = pulse
	l.mu.Unlock()
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

// follow 把序号 after 之后的事件帧写给客户端并跟随到事件写完、客户端断开或写出失败，等待新事件时按心跳间隔调用 heartbeat
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
			if err := l.heartbeat(w); err != nil {
				return err
			}
		}
	}
}

// heartbeat 调用 pulse 记录一个新的状态事件，由跟随循环写给客户端；没有 pulse 时给该跟随者写 SSE 注释
func (l *streamLog) heartbeat(w http.ResponseWriter) error {
	l.mu.Lock()
	pulse := l.pulse
	l.mu.Unlock()
	if pulse == nil {
		return writeSSEHeartbeat(w)
	}
	return pulse()
}
