package api

import (
	"io"
	"net/http"
	"sync"
	"time"
)

// 流式响应的心跳帧：OpenAI 与 Interactions 为 SSE 注释，Responses 写出开场事件后改由写出器以新序号写出状态事件，Anthropic 为官方 ping 事件，Gemini 为不触发事件的空行
const (
	sseHeartbeat       = ": ping\n\n"
	anthropicHeartbeat = "event: ping\ndata: {\"type\":\"ping\"}\n\n"
	geminiHeartbeat    = "\n"
)

// streamKeepalive 是流式响应的 ResponseWriter，距最近一次写出满心跳间隔时写出心跳帧或调用 pulse，响应头由 open 或首个心跳写出
type streamKeepalive struct {
	http.ResponseWriter
	frame   string
	pulse   func() error
	mu      sync.Mutex
	timer   *time.Timer
	opened  bool
	stopped bool
}

// keepStreamAlive 返回静默满心跳间隔时写出 frame 的流式 ResponseWriter，首个事件可能晚于心跳间隔的流式响应用它在等待期间写出响应头与心跳
func keepStreamAlive(w http.ResponseWriter, frame string) *streamKeepalive {
	writer := &streamKeepalive{ResponseWriter: w, frame: frame}
	writer.mu.Lock()
	writer.timer = time.AfterFunc(streamHeartbeatInterval, writer.beat)
	writer.mu.Unlock()
	return writer
}

// openStream 写出流式响应头并返回静默满心跳间隔时写出 frame 的 ResponseWriter，响应头写出失败时停止心跳
func openStream(w http.ResponseWriter, frame string) (*streamKeepalive, error) {
	writer := keepStreamAlive(w, frame)
	if err := writer.open(); err != nil {
		writer.stop()
		return writer, err
	}
	return writer, nil
}

// setPulse 让之后的心跳改为调用 pulse，pulse 经 Write 写出并推迟下一次心跳
func (writer *streamKeepalive) setPulse(pulse func() error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.pulse = pulse
}

// open 写出流式响应头，已写出时不再写
func (writer *streamKeepalive) open() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.openLocked()
}

// openLocked 在持有锁时写出流式响应头，已写出时不再写
func (writer *streamKeepalive) openLocked() error {
	if writer.opened {
		return nil
	}
	writer.opened = true
	return streamHeaders(writer.ResponseWriter)
}

// beat 写出心跳帧或调用 pulse，并安排下一次心跳
func (writer *streamKeepalive) beat() {
	writer.mu.Lock()
	if pulse := writer.pulse; pulse != nil && !writer.stopped {
		writer.mu.Unlock()
		_ = pulse()
		return
	}
	defer writer.mu.Unlock()
	if writer.stopped || writer.openLocked() != nil {
		return
	}
	if _, err := io.WriteString(writer.ResponseWriter, writer.frame); err != nil {
		return
	}
	if http.NewResponseController(writer.ResponseWriter).Flush() != nil {
		return
	}
	writer.timer.Reset(streamHeartbeatInterval)
}

// stop 停止心跳，返回响应头是否已写出
func (writer *streamKeepalive) stop() bool {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.stopped = true
	writer.timer.Stop()
	return writer.opened
}

// Write 写出数据并把下一次心跳推迟到心跳间隔之后
func (writer *streamKeepalive) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if !writer.stopped {
		writer.timer.Reset(streamHeartbeatInterval)
	}
	return writer.ResponseWriter.Write(data)
}

// FlushError 与心跳互斥地刷新底层 ResponseWriter
func (writer *streamKeepalive) FlushError() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return http.NewResponseController(writer.ResponseWriter).Flush()
}

// Unwrap 返回底层 ResponseWriter
func (writer *streamKeepalive) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

// nonstreamKeepaliveWriter 在非流式响应写出前每隔心跳间隔写出一个换行，首个换行以 200 与 JSON 类型提交响应头
type nonstreamKeepaliveWriter struct {
	http.ResponseWriter
	mu        sync.Mutex
	timer     *time.Timer
	committed bool
	stopped   bool
}

// nonstreamKeepalive 在开启非流式保活时返回带保活的 ResponseWriter，以及在处理函数返回前调用的停止函数
func (s *server) nonstreamKeepalive(w http.ResponseWriter) (http.ResponseWriter, func()) {
	if s.responseModes == nil || !s.responseModes().NonstreamKeepalive {
		return w, func() {}
	}
	writer := &nonstreamKeepaliveWriter{ResponseWriter: w}
	writer.timer = time.AfterFunc(streamHeartbeatInterval, writer.beat)
	return writer, writer.stop
}

// fakeStream 判断流式请求是否先按非流式取得完整结果再以流式输出
func (s *server) fakeStream() bool {
	return s.responseModes != nil && s.responseModes().FakeStream
}

// beat 写出一个换行并安排下一次保活
func (writer *nonstreamKeepaliveWriter) beat() {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.stopped {
		return
	}
	if !writer.committed {
		writer.ResponseWriter.Header().Set("Content-Type", "application/json")
		writer.ResponseWriter.Header().Set("X-Accel-Buffering", "no")
		writer.ResponseWriter.WriteHeader(http.StatusOK)
		writer.committed = true
	}
	if _, err := io.WriteString(writer.ResponseWriter, "\n"); err != nil {
		return
	}
	if http.NewResponseController(writer.ResponseWriter).Flush() != nil {
		return
	}
	writer.timer.Reset(streamHeartbeatInterval)
}

// stop 停止保活，之后的写入直接到达底层 ResponseWriter
func (writer *nonstreamKeepaliveWriter) stop() {
	writer.mu.Lock()
	writer.stopped = true
	writer.timer.Stop()
	writer.mu.Unlock()
}

// Header 停止保活后返回响应头，处理函数只在写出最终响应前访问它
func (writer *nonstreamKeepaliveWriter) Header() http.Header {
	writer.stop()
	return writer.ResponseWriter.Header()
}

// WriteHeader 在响应头未提交时写出状态码，已提交时保持 200，错误由正文的协议错误对象表达
func (writer *nonstreamKeepaliveWriter) WriteHeader(status int) {
	writer.stop()
	if !writer.committed {
		writer.ResponseWriter.WriteHeader(status)
	}
}

// Write 停止保活后写出数据
func (writer *nonstreamKeepaliveWriter) Write(data []byte) (int, error) {
	writer.stop()
	return writer.ResponseWriter.Write(data)
}

// Unwrap 返回底层 ResponseWriter
func (writer *nonstreamKeepaliveWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}
