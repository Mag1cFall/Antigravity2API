// Package requestdb 在本地 SQLite 中保存公开 API 的请求记录、小时与本地日汇总、正文、上游尝试正文、额度快照与 WARN 以上的服务日志
package requestdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// Retention 是请求记录、汇总、额度快照与日志的保留时长
const Retention = 90 * 24 * time.Hour

// BodyLimit 是单个请求或响应正文保存的最大字节数
const BodyLimit = 64 << 10

// bodyRetention 是公开协议正文与上游尝试正文各自保留的最近条数
const bodyRetention = 1000

// batchLimit 是单个写事务合并的最大条数
const batchLimit = 256

// queueLimit 是等待写入的最大条数，队列已满时丢弃新条目
const queueLimit = 4096

// hourMS 是一小时的毫秒数
const hourMS = int64(time.Hour / time.Millisecond)

// rollupColumns 是小时与本地日汇总表的列，start 是分段起点
const rollupColumns = `
 start INTEGER NOT NULL, model TEXT NOT NULL, account TEXT NOT NULL, api_key TEXT NOT NULL, protocol TEXT NOT NULL,
 state TEXT NOT NULL, quota_group TEXT NOT NULL, status INTEGER NOT NULL, requests INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, cached_tokens INTEGER NOT NULL, reasoning_tokens INTEGER NOT NULL, reply_tokens INTEGER NOT NULL,
 duration_ms INTEGER NOT NULL, first_event_ms INTEGER NOT NULL, first_events INTEGER NOT NULL, queue_ms INTEGER NOT NULL,
 cost_usd REAL NOT NULL, unpriced INTEGER NOT NULL, duration_hist BLOB NOT NULL, first_event_hist BLOB NOT NULL, last_time INTEGER NOT NULL,
 PRIMARY KEY (start, model, account, api_key, protocol, state, quota_group, status)`

const schema = `
CREATE TABLE IF NOT EXISTS requests (
 id TEXT PRIMARY KEY, time INTEGER NOT NULL, client_request_id TEXT NOT NULL, response_id TEXT NOT NULL,
 protocol TEXT NOT NULL, path TEXT NOT NULL, api_key TEXT NOT NULL, client_ip TEXT NOT NULL, user_agent TEXT NOT NULL,
 stream INTEGER NOT NULL, model TEXT NOT NULL, upstream_model TEXT NOT NULL, model_version TEXT NOT NULL,
 request_type TEXT NOT NULL, prompt_tier TEXT NOT NULL, cascade_id TEXT NOT NULL, step_index INTEGER NOT NULL,
 account TEXT NOT NULL, quota_group TEXT NOT NULL, status INTEGER NOT NULL, state TEXT NOT NULL,
 error_phase TEXT NOT NULL, upstream_status INTEGER NOT NULL, error_code TEXT NOT NULL,
 duration_ms INTEGER NOT NULL, first_event_ms INTEGER NOT NULL, queue_ms INTEGER NOT NULL,
 input_tokens INTEGER NOT NULL, cached_tokens INTEGER NOT NULL, reasoning_tokens INTEGER NOT NULL, reply_tokens INTEGER NOT NULL,
 tool_calls INTEGER NOT NULL, cost_usd REAL, error TEXT NOT NULL, attempts TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS requests_time ON requests(time);
CREATE INDEX IF NOT EXISTS requests_model_time ON requests(model, time);
CREATE INDEX IF NOT EXISTS requests_account_time ON requests(account, time);
CREATE INDEX IF NOT EXISTS requests_state_time ON requests(state, time);
CREATE TABLE IF NOT EXISTS hours (` + rollupColumns + `) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS days (` + rollupColumns + `) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS bodies (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE, time INTEGER NOT NULL,
 request TEXT NOT NULL, request_size INTEGER NOT NULL, response TEXT NOT NULL, response_size INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS upstream (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL, attempt INTEGER NOT NULL, time INTEGER NOT NULL,
 request TEXT NOT NULL, request_size INTEGER NOT NULL, response TEXT NOT NULL, response_size INTEGER NOT NULL,
 UNIQUE (id, attempt));
CREATE TABLE IF NOT EXISTS quota_snapshots (
 time INTEGER NOT NULL, account TEXT NOT NULL, bucket_id TEXT NOT NULL, window TEXT NOT NULL,
 remaining_fraction REAL NOT NULL, reset_time INTEGER);
CREATE INDEX IF NOT EXISTS quota_snapshots_account_time ON quota_snapshots(account, time);
CREATE TABLE IF NOT EXISTS logs (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, time INTEGER NOT NULL, level TEXT NOT NULL, source TEXT NOT NULL,
 event TEXT NOT NULL, message TEXT NOT NULL, request_id TEXT NOT NULL, model TEXT NOT NULL, data TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS logs_time ON logs(time);
CREATE INDEX IF NOT EXISTS logs_request_id ON logs(request_id);`

// Attempt 是一次上游生成尝试，Request 与 Response 是发往上游的请求正文与上游原始响应
type Attempt struct {
	Account string `json:"account"`
	URL     string `json:"url"`
	// Status 是上游 HTTP 状态码，连接失败时为 0
	Status int `json:"status"`
	// Code 是上游错误的 ErrorInfo.reason，没有时为 error.status
	Code string `json:"code,omitempty"`
	// RetryDelay 是上游 RetryInfo.retryDelay
	RetryDelay string `json:"retry_delay,omitempty"`
	// TraceID 是上游响应的 traceId
	TraceID string `json:"trace_id,omitempty"`
	// ResponseID 是上游响应的 responseId
	ResponseID string `json:"response_id,omitempty"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Request    []byte `json:"-"`
	Response   []byte `json:"-"`
}

// Row 是一条请求记录，Time 为完成时间，进行中的请求为开始时间
type Row struct {
	// ID 是服务生成的请求 ID，与响应头 X-Request-ID 相同
	ID              string    `json:"id"`
	Time            time.Time `json:"time"`
	ClientRequestID string    `json:"client_request_id"`
	// ResponseID 是公开协议响应中的 ID
	ResponseID    string `json:"response_id"`
	Protocol      string `json:"protocol"`
	Path          string `json:"path"`
	APIKey        string `json:"api_key"`
	ClientIP      string `json:"client_ip"`
	UserAgent     string `json:"user_agent"`
	Stream        bool   `json:"stream"`
	Model         string `json:"model"`
	UpstreamModel string `json:"upstream_model"`
	ModelVersion  string `json:"model_version"`
	// RequestType 是上游信封的 requestType
	RequestType string `json:"request_type"`
	PromptTier  string `json:"prompt_tier"`
	CascadeID   string `json:"cascade_id"`
	StepIndex   int    `json:"step_index"`
	Account     string `json:"account"`
	QuotaGroup  string `json:"quota_group"`
	Status      int    `json:"status"`
	State       string `json:"state"`
	// ErrorPhase 是失败所在阶段：request、routing、upstream、network、timeout、internal
	ErrorPhase      string `json:"error_phase"`
	UpstreamStatus  int    `json:"upstream_status"`
	ErrorCode       string `json:"error_code"`
	DurationMS      int64  `json:"duration_ms"`
	FirstEventMS    int64  `json:"first_event_ms"`
	QueueMS         int64  `json:"queue_ms"`
	InputTokens     int64  `json:"input_tokens"`
	CachedTokens    int64  `json:"cached_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	ReplyTokens     int64  `json:"reply_tokens"`
	ToolCalls       int    `json:"tool_calls"`
	// CostUSD 是按上游模型官方单价估算的美元费用，模型没有单价时为 null
	CostUSD  *float64  `json:"cost_usd"`
	Error    string    `json:"error"`
	Attempts []Attempt `json:"attempts"`
}

// Body 是截断到 BodyLimit 的请求与响应正文，Size 为原始字节数
type Body struct {
	Request      string `json:"request"`
	RequestSize  int64  `json:"request_size"`
	Response     string `json:"response"`
	ResponseSize int64  `json:"response_size"`
}

// NewBody 截断请求与响应正文并记录原始字节数
func NewBody(request, response []byte) Body {
	return Body{
		Request: string(request[:min(len(request), BodyLimit)]), RequestSize: int64(len(request)),
		Response: string(response[:min(len(response), BodyLimit)]), ResponseSize: int64(len(response)),
	}
}

// Entry 是一次完成请求写入账本的全部内容
type Entry struct {
	Row Row
	// Body 是公开协议的请求与响应正文
	Body *Body
	// Upstream 是与 Row.Attempts 一一对应的上游尝试正文
	Upstream []Body
}

// QuotaSnapshot 是一个账户的一个额度桶在某一时刻的剩余比例
type QuotaSnapshot struct {
	Time              time.Time  `json:"time"`
	Account           string     `json:"account"`
	BucketID          string     `json:"bucket_id"`
	Window            string     `json:"window"`
	RemainingFraction float64    `json:"remaining_fraction"`
	ResetTime         *time.Time `json:"reset_time,omitempty"`
}

// Log 是一条服务日志，Request 是请求开始与结束日志附带的请求记录
type Log struct {
	Seq       int64          `json:"seq,omitempty"`
	Time      time.Time      `json:"time"`
	Level     string         `json:"level"`
	Source    string         `json:"source"`
	Event     string         `json:"event,omitempty"`
	Message   string         `json:"message"`
	RequestID string         `json:"request_id,omitempty"`
	Model     string         `json:"model,omitempty"`
	Request   *Row           `json:"request,omitempty"`
	Attrs     map[string]any `json:"attrs,omitempty"`
}

// write 是写入协程处理的一条请求、一组额度快照或一条日志
type write struct {
	entry *Entry
	quota []QuotaSnapshot
	log   *Log
}

// Store 串行批量写入请求账本，并提供汇总、记录、正文、额度快照与日志查询
type Store struct {
	db      *sql.DB
	writes  chan write
	done    chan struct{}
	capture atomic.Bool
	dropped atomic.Int64
	mu      sync.RWMutex
	closed  bool
}

// Open 打开或创建账本数据库并启动写入协程
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	options := url.Values{"_pragma": {"busy_timeout(10000)", "journal_mode(WAL)", "synchronous(NORMAL)"}}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+options.Encode())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	if _, err := db.Exec(schema); err != nil {
		return nil, errors.Join(fmt.Errorf("初始化请求账本: %w", err), db.Close())
	}
	store := &Store{db: db, writes: make(chan write, queueLimit), done: make(chan struct{})}
	if err := store.alignDays(); err != nil {
		return nil, errors.Join(fmt.Errorf("重建本地日汇总: %w", err), db.Close())
	}
	go store.run()
	return store, nil
}

// Close 写完已排队的条目后关闭数据库
func (store *Store) Close() error {
	store.mu.Lock()
	if store.closed {
		store.mu.Unlock()
		return nil
	}
	store.closed = true
	close(store.writes)
	store.mu.Unlock()
	<-store.done
	return store.db.Close()
}

// SetBodyCapture 设置是否保存公开协议正文与成功请求的上游尝试正文
func (store *Store) SetBodyCapture(enabled bool) {
	store.capture.Store(enabled)
}

// BodyCapture 返回是否保存公开协议正文与成功请求的上游尝试正文
func (store *Store) BodyCapture() bool {
	return store.capture.Load()
}

// Record 排队写入一条请求，不等待落盘
func (store *Store) Record(entry Entry) {
	entry.Row.CostUSD = rowCost(&entry.Row)
	store.enqueue(write{entry: &entry})
}

// RecordQuota 排队写入账户本次取得的全部额度桶，不等待落盘
func (store *Store) RecordQuota(account string, groups []antigravity.QuotaGroup) {
	now := time.Now().UTC()
	var snapshots []QuotaSnapshot
	for _, group := range groups {
		for _, bucket := range group.Buckets {
			snapshot := QuotaSnapshot{Time: now, Account: account, BucketID: bucket.BucketID, Window: bucket.Window, RemainingFraction: bucket.RemainingFraction}
			if reset, err := time.Parse(time.RFC3339, bucket.ResetTime); err == nil {
				reset = reset.UTC()
				snapshot.ResetTime = &reset
			}
			snapshots = append(snapshots, snapshot)
		}
	}
	if len(snapshots) != 0 {
		store.enqueue(write{quota: snapshots})
	}
}

// RecordLog 排队写入一条日志，不等待落盘
func (store *Store) RecordLog(entry Log) {
	store.enqueue(write{log: &entry})
}

// enqueue 把条目交给写入协程，队列已满时计入丢弃数而不阻塞请求
func (store *Store) enqueue(item write) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	if store.closed {
		return
	}
	select {
	case store.writes <- item:
	default:
		store.dropped.Add(1)
	}
}

// run 合并连续到达的条目为单个事务，并每天清理过期数据
func (store *Store) run() {
	defer close(store.done)
	store.prune()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case item, ok := <-store.writes:
			if !ok {
				return
			}
			batch := []write{item}
		collect:
			for len(batch) < batchLimit {
				select {
				case next, ok := <-store.writes:
					if !ok {
						store.commit(batch)
						return
					}
					batch = append(batch, next)
				default:
					break collect
				}
			}
			store.commit(batch)
		case <-ticker.C:
			store.prune()
		}
	}
}

// commit 写入一批条目，输出写入失败与队列丢弃
func (store *Store) commit(batch []write) {
	if err := store.write(batch); err != nil {
		slog.Error(fmt.Sprintf("请求账本写入失败 | 条目=%d | 错误=%v", len(batch), err), "source", "service")
	}
	if dropped := store.dropped.Swap(0); dropped > 0 {
		slog.Warn(fmt.Sprintf("请求账本写入队列已满 | 丢弃=%d", dropped), "source", "service")
	}
}

// write 在一个事务内写入请求、汇总、正文、额度快照与日志，并把两类正文裁剪到保留条数
func (store *Store) write(batch []write) (err error) {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	hours, days := map[rollupKey]*entry{}, map[rollupKey]*entry{}
	trimmed := map[string]bool{}
	for _, item := range batch {
		for _, snapshot := range item.quota {
			var reset any
			if snapshot.ResetTime != nil {
				reset = snapshot.ResetTime.UnixMilli()
			}
			if _, err := tx.Exec(`INSERT INTO quota_snapshots VALUES (?,?,?,?,?,?)`,
				snapshot.Time.UnixMilli(), snapshot.Account, snapshot.BucketID, snapshot.Window, snapshot.RemainingFraction, reset); err != nil {
				return err
			}
		}
		if entry := item.log; entry != nil {
			data, err := json.Marshal(logData{Request: entry.Request, Attrs: entry.Attrs})
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO logs (time,level,source,event,message,request_id,model,data) VALUES (?,?,?,?,?,?,?,?)`,
				entry.Time.UnixMilli(), entry.Level, entry.Source, entry.Event, entry.Message, entry.RequestID, entry.Model, string(data)); err != nil {
				return err
			}
		}
		if item.entry == nil {
			continue
		}
		inserted, err := insertRow(tx, item.entry.Row)
		if err != nil {
			return err
		}
		if inserted {
			value := rowEntry(&item.entry.Row)
			day, _ := localDay(value.time)
			collect(hours, keyOf(floorDiv(value.time, hourMS)*hourMS, value), value)
			collect(days, keyOf(day, value), value)
		}
		row := item.entry.Row
		if body := item.entry.Body; body != nil {
			trimmed["bodies"] = true
			if _, err := tx.Exec(`INSERT OR REPLACE INTO bodies (id,time,request,request_size,response,response_size) VALUES (?,?,?,?,?,?)`,
				row.ID, row.Time.UnixMilli(), body.Request, body.RequestSize, body.Response, body.ResponseSize); err != nil {
				return err
			}
		}
		for index, body := range item.entry.Upstream {
			trimmed["upstream"] = true
			if _, err := tx.Exec(`INSERT OR REPLACE INTO upstream (id,attempt,time,request,request_size,response,response_size) VALUES (?,?,?,?,?,?,?)`,
				row.ID, index, row.Time.UnixMilli(), body.Request, body.RequestSize, body.Response, body.ResponseSize); err != nil {
				return err
			}
		}
	}
	for key, value := range hours {
		if err := mergeRollup(tx, "hours", key, value); err != nil {
			return err
		}
	}
	for key, value := range days {
		if err := mergeRollup(tx, "days", key, value); err != nil {
			return err
		}
	}
	for table := range trimmed {
		if _, err := tx.Exec(`DELETE FROM `+table+` WHERE seq <= (SELECT MAX(seq) FROM `+table+`) - ?`, bodyRetention); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// insertRow 写入一条请求记录并返回是否为新记录
func insertRow(tx *sql.Tx, row Row) (bool, error) {
	attempts := row.Attempts
	if attempts == nil {
		attempts = []Attempt{}
	}
	encoded, err := json.Marshal(attempts)
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(`INSERT OR IGNORE INTO requests VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		row.ID, row.Time.UnixMilli(), row.ClientRequestID, row.ResponseID, row.Protocol, row.Path, row.APIKey, row.ClientIP,
		row.UserAgent, row.Stream, row.Model, row.UpstreamModel, row.ModelVersion, row.RequestType, row.PromptTier, row.CascadeID,
		row.StepIndex, row.Account, row.QuotaGroup, row.Status, row.State, row.ErrorPhase, row.UpstreamStatus, row.ErrorCode,
		row.DurationMS, row.FirstEventMS, row.QueueMS, row.InputTokens, row.CachedTokens, row.ReasoningTokens, row.ReplyTokens,
		row.ToolCalls, row.CostUSD, row.Error, string(encoded))
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	return inserted == 1, err
}

// mergeRollup 把本批记录累加到汇总表中已有的同键汇总
func mergeRollup(tx *sql.Tx, table string, key rollupKey, value *entry) error {
	existing := &entry{}
	var durations, firstEvents []byte
	err := tx.QueryRow(`SELECT requests, input_tokens, cached_tokens, reasoning_tokens, reply_tokens, duration_ms, first_event_ms,
 first_events, queue_ms, cost_usd, unpriced, duration_hist, first_event_hist, last_time FROM `+table+`
 WHERE start=? AND model=? AND account=? AND api_key=? AND protocol=? AND state=? AND quota_group=? AND status=?`,
		key.start, key.model, key.account, key.apiKey, key.protocol, key.state, key.quotaGroup, key.status).Scan(
		&existing.requests, &existing.inputTokens, &existing.cachedTokens, &existing.reasoningTokens, &existing.replyTokens,
		&existing.durationMS, &existing.firstEventMS, &existing.firstEvents, &existing.queueMS, &existing.costUSD, &existing.unpriced, &durations, &firstEvents, &existing.lastTime)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		if existing.duration, err = decodeCounts(durations); err != nil {
			return err
		}
		if existing.firstEvent, err = decodeCounts(firstEvents); err != nil {
			return err
		}
		value.combine(existing)
	}
	_, err = tx.Exec(`INSERT OR REPLACE INTO `+table+` VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		key.start, key.model, key.account, key.apiKey, key.protocol, key.state, key.quotaGroup, key.status, value.requests,
		value.inputTokens, value.cachedTokens, value.reasoningTokens, value.replyTokens,
		value.durationMS, value.firstEventMS, value.firstEvents, value.queueMS, value.costUSD, value.unpriced,
		value.duration.encode(), value.firstEvent.encode(), value.lastTime)
	return err
}

// prune 删除超过保留期的数据，记录与汇总都从截止时间所在本地日的起点开始保留，使三层数据覆盖同一范围
func (store *Store) prune() {
	day, _ := localDay(time.Now().Add(-Retention).UnixMilli())
	hour := floorDiv(day, hourMS) * hourMS
	var errs []error
	for _, statement := range []string{
		`DELETE FROM requests WHERE time < ?`, `DELETE FROM hours WHERE start < ?`, `DELETE FROM bodies WHERE time < ?`,
		`DELETE FROM upstream WHERE time < ?`, `DELETE FROM quota_snapshots WHERE time < ?`, `DELETE FROM logs WHERE time < ?`,
	} {
		_, err := store.db.Exec(statement, hour)
		errs = append(errs, err)
	}
	_, err := store.db.Exec(`DELETE FROM days WHERE start < ?`, day)
	if err := errors.Join(append(errs, err)...); err != nil {
		slog.Error("请求账本清理失败 | 错误="+err.Error(), "source", "service")
	}
}

// alignDays 在本地日汇总的起点与当前服务器时区不一致时，从小时汇总与原始记录重建全部本地日汇总
func (store *Store) alignDays() (err error) {
	rows, err := store.db.Query(`SELECT start FROM days GROUP BY start`)
	if err != nil {
		return err
	}
	aligned := true
	for rows.Next() {
		var start int64
		if err := rows.Scan(&start); err != nil {
			return errors.Join(err, rows.Close())
		}
		if day, _ := localDay(start); day != start {
			aligned = false
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil || aligned {
		return err
	}
	var earliest sql.NullInt64
	if err := store.db.QueryRow(`SELECT MIN(start) FROM hours`).Scan(&earliest); err != nil {
		return err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, tx.Rollback())
		}
	}()
	if _, err := tx.Exec(`DELETE FROM days`); err != nil {
		return err
	}
	rebuilt, now := 0, time.Now().UnixMilli()
	for start, end := localDay(earliest.Int64); earliest.Valid && start <= now; start, end = localDay(end) {
		days := map[rollupKey]*entry{}
		for _, part := range planHours(start, end, nil) {
			if err := store.scan(context.Background(), part, nil, func(value *entry) { collect(days, keyOf(start, value), value) }); err != nil {
				return err
			}
		}
		for key, value := range days {
			if err := mergeRollup(tx, "days", key, value); err != nil {
				return err
			}
		}
		rebuilt++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	slog.Info(fmt.Sprintf("请求账本已按服务器时区重建本地日汇总 | 天数=%d", rebuilt), "source", "service")
	return nil
}

// floorDiv 返回向负无穷取整的整数除法结果
func floorDiv(value, divisor int64) int64 {
	quotient := value / divisor
	if value%divisor != 0 && (value < 0) != (divisor < 0) {
		quotient--
	}
	return quotient
}
