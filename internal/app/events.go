package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/logging"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

const (
	// logRetain 是内存中保留的日志条数
	logRetain = 2000
	// logCompactAt 是触发裁剪的日志条数
	logCompactAt = 2200
	// logInitial 是新连接收到的最近日志条数
	logInitial = 200
	// subscriberBuffer 是每个事件流连接的缓冲事件数，堆积超过时断开连接
	subscriberBuffer = 1024
	// pingInterval 是事件流空闲时发送注释帧的间隔
	pingInterval = 30 * time.Second
)

// event 是管理事件流中的一条事件，Type 为 accounts、log、request 或 login
type event struct {
	Type string `json:"type"`
	Data any    `json:"data"`
}

// activeRequest 是一个进行中的公开 POST 请求
type activeRequest struct {
	trace  *requestdb.Trace
	cancel context.CancelCauseFunc
	// relays 等待生成事件转发协程结束，使记录包含断开后排空的事件
	relays sync.WaitGroup
}

// hub 保存最新账户列表、最近日志与进行中的请求，并向事件流连接分发事件
type hub struct {
	mu          sync.Mutex
	accounts    *event
	encoded     []byte
	logs        []requestdb.Log
	active      map[string]*activeRequest
	subscribers map[chan event]struct{}
}

// newHub 创建空的事件中心
func newHub() *hub {
	return &hub{active: map[string]*activeRequest{}, subscribers: map[chan event]struct{}{}}
}

// publishLocked 在持锁期间分发事件，缓冲已满的连接被关闭
func (h *hub) publishLocked(item event) {
	for subscriber := range h.subscribers {
		select {
		case subscriber <- item:
		default:
			delete(h.subscribers, subscriber)
			close(subscriber)
		}
	}
}

// publish 向全部连接分发一条事件
func (h *hub) publish(item event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(item)
}

// setAccounts 更新账户列表，内容变化时分发
func (h *hub) setAccounts(accounts []account) {
	encoded, _ := json.Marshal(accounts)
	h.mu.Lock()
	defer h.mu.Unlock()
	if bytes.Equal(h.encoded, encoded) {
		return
	}
	h.accounts, h.encoded = &event{Type: "accounts", Data: accounts}, encoded
	h.publishLocked(*h.accounts)
}

// log 保存一条日志并分发
func (h *hub) log(entry requestdb.Log) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, entry)
	if len(h.logs) >= logCompactAt {
		h.logs = slices.Clone(h.logs[len(h.logs)-logRetain:])
	}
	h.publishLocked(event{Type: "log", Data: entry})
}

// clearLogs 清空内存中的日志
func (h *hub) clearLogs() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = nil
}

// track 登记进行中的请求，供列表与停止使用
func (h *hub) track(id string, active *activeRequest) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.active[id] = active
}

// begin 分发进行中请求的记录：生成开始时与执行账户变化时
func (h *hub) begin(row requestdb.Row) {
	h.publish(event{Type: "request", Data: row})
}

// end 移除进行中的请求并分发结束记录
func (h *hub) end(id string, row requestdb.Row) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.active, id)
	h.publishLocked(event{Type: "request", Data: row})
}

// running 按开始时间返回进行中请求的记录
func (h *hub) running() []requestdb.Row {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runningLocked()
}

// runningLocked 在持锁期间按开始时间返回进行中请求的记录
func (h *hub) runningLocked() []requestdb.Row {
	rows := make([]requestdb.Row, 0, len(h.active))
	for _, active := range h.active {
		rows = append(rows, active.trace.Snapshot())
	}
	slices.SortFunc(rows, func(a, b requestdb.Row) int { return a.Time.Compare(b.Time) })
	return rows
}

// stop 以管理端停止为原因取消进行中的请求，请求不存在时返回 false
func (h *hub) stop(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	active := h.active[id]
	if active != nil {
		active.cancel(requestdb.ErrCancelledByAdmin)
	}
	return active != nil
}

// subscribe 返回新连接的事件通道，先放入账户列表、最近日志与进行中的请求
func (h *hub) subscribe() chan event {
	h.mu.Lock()
	defer h.mu.Unlock()
	rows := h.runningLocked()
	logs := h.logs[max(len(h.logs)-logInitial, 0):]
	subscriber := make(chan event, 1+len(logs)+len(rows)+subscriberBuffer)
	if h.accounts != nil {
		subscriber <- *h.accounts
	}
	for _, entry := range logs {
		subscriber <- event{Type: "log", Data: entry}
	}
	for _, row := range rows {
		subscriber <- event{Type: "request", Data: row}
	}
	h.subscribers[subscriber] = struct{}{}
	return subscriber
}

// unsubscribe 关闭仍在登记中的连接通道
func (h *hub) unsubscribe(subscriber chan event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subscribers[subscriber]; ok {
		delete(h.subscribers, subscriber)
		close(subscriber)
	}
}

// streamEvents 以 SSE 推送管理事件，空闲时发送注释帧保持连接
func (s *server) streamEvents(w http.ResponseWriter, r *http.Request) {
	subscriber := s.events.subscribe()
	defer s.events.unsubscribe(subscriber)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	if controller.Flush() != nil {
		return
	}
	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case item, ok := <-subscriber:
			if !ok {
				return
			}
			data, _ := json.Marshal(item)
			if _, err := w.Write(append(append([]byte("data: "), data...), "\n\n"...)); err != nil || controller.Flush() != nil {
				return
			}
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil || controller.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		}
	}
}

// handler 返回把日志写入事件中心、WARN 以上同时写入账本的 slog 处理器，级别阈值与服务日志一致
func (h *hub) handler(level slog.Leveler, ledger *requestdb.Store) slog.Handler {
	return &logHandler{hub: h, ledger: ledger, level: level}
}

// withoutBodies 返回去掉上游请求与响应正文的尝试记录，内存中的日志不持有正文
func withoutBodies(attempts []requestdb.Attempt) []requestdb.Attempt {
	attempts = slices.Clone(attempts)
	for index := range attempts {
		attempts[index].Request, attempts[index].Response = nil, nil
	}
	return attempts
}

// logHandler 把 slog 记录转换为 requestdb.Log；source、event、request_id、model 与 request 属性进入同名字段，其余属性放入 attrs
type logHandler struct {
	hub    *hub
	ledger *requestdb.Store
	level  slog.Leveler
	attrs  []slog.Attr
	prefix string
}

// Enabled 按服务日志级别过滤
func (handler *logHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level.Level()
}

// Handle 转换并保存一条日志；请求 ID 优先取 context，没有 source 属性时以 service 作为来源
func (handler *logHandler) Handle(ctx context.Context, record slog.Record) error {
	entry := requestdb.Log{Time: record.Time.UTC(), Level: logLevel(record.Level), Source: "service", Message: record.Message}
	attrs := map[string]any{}
	visit := func(attr slog.Attr) bool {
		value := attr.Value.Resolve()
		switch attr.Key {
		case "source":
			entry.Source = value.String()
		case "event":
			entry.Event = value.String()
		case "request_id":
			entry.RequestID = value.String()
		case "model":
			entry.Model = value.String()
		case "request":
			if row, ok := value.Any().(requestdb.Row); ok {
				row.Attempts = withoutBodies(row.Attempts)
				entry.Request = &row
				entry.RequestID = row.ID
				entry.Model = row.Model
				return true
			}
			attrs[attr.Key] = attrValue(value)
		default:
			attrs[attr.Key] = attrValue(value)
		}
		return true
	}
	for _, attr := range handler.attrs {
		visit(attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		attr.Key = handler.prefix + attr.Key
		return visit(attr)
	})
	if id := logging.RequestID(ctx); id != "" {
		entry.RequestID = id
	}
	if len(attrs) != 0 {
		entry.Attrs = attrs
	}
	handler.hub.log(entry)
	if record.Level >= slog.LevelWarn {
		handler.ledger.RecordLog(entry)
	}
	return nil
}

// WithAttrs 返回附带固定属性的处理器
func (handler *logHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *handler
	next.attrs = slices.Clone(handler.attrs)
	for _, attr := range attrs {
		attr.Key = handler.prefix + attr.Key
		next.attrs = append(next.attrs, attr)
	}
	return &next
}

// WithGroup 返回以组名作为属性名前缀的处理器
func (handler *logHandler) WithGroup(name string) slog.Handler {
	next := *handler
	next.prefix = handler.prefix + name + "."
	return &next
}

// logLevel 把 slog 级别归入 DEBUG、INFO、WARN、ERROR
func logLevel(level slog.Level) string {
	switch {
	case level < slog.LevelInfo:
		return "DEBUG"
	case level < slog.LevelWarn:
		return "INFO"
	case level < slog.LevelError:
		return "WARN"
	}
	return "ERROR"
}

// attrValue 把属性值转换为可 JSON 编码的值，错误与时长转为文本，组转为对象
func attrValue(value slog.Value) any {
	switch value.Kind() {
	case slog.KindGroup:
		group := map[string]any{}
		for _, attr := range value.Group() {
			group[attr.Key] = attrValue(attr.Value.Resolve())
		}
		return group
	case slog.KindDuration:
		return value.Duration().String()
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return err.Error()
		}
	}
	return value.Any()
}
