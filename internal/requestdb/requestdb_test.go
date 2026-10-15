package requestdb

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"math"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
)

// openStore 打开测试账本
func openStore(t *testing.T, path string) *Store {
	t.Helper()
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// reopen 关闭账本使排队条目落盘后重新打开
func reopen(t *testing.T, store *Store, path string) *Store {
	t.Helper()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	return openStore(t, path)
}

// TestRecordsQueryAndDetail 验证维度筛选、状态码、关键字、游标分页、CSV 导出，以及公开正文与逐次上游正文的读取
func TestRecordsQueryAndDetail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	store := openStore(t, path)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index := range 5 {
		row := Row{
			ID: fmt.Sprintf("req_%d", index), Time: now.Add(-time.Duration(index) * time.Minute), ClientRequestID: fmt.Sprintf("client-%d", index),
			ResponseID: fmt.Sprintf("chatcmpl_%d", index), Protocol: "openai-chat", Path: "/v1/chat/completions", APIKey: "sk-…abcd",
			ClientIP: "127.0.0.1", UserAgent: "test", Stream: index%2 == 0, Model: "gemini-pro", UpstreamModel: "gemini-pro-high",
			ModelVersion: "gemini-pro-001", RequestType: "agent", PromptTier: "full", CascadeID: fmt.Sprintf("cascade-%d", index), StepIndex: index,
			Account: "a@example.com", QuotaGroup: "gemini", Status: 200, State: StateCompleted,
			DurationMS: 1500, FirstEventMS: 300, QueueMS: 5, InputTokens: 10, CachedTokens: 4, ReasoningTokens: 3, ReplyTokens: 7,
		}
		entry := Entry{Row: row}
		if index == 3 {
			entry.Row.Account, entry.Row.QuotaGroup, entry.Row.Status, entry.Row.State = "b@example.com", "3p", 429, StateFailed
			entry.Row.Error, entry.Row.ErrorPhase, entry.Row.UpstreamStatus, entry.Row.ErrorCode = "=HYPERLINK(1)", "upstream", 429, "RATE_LIMIT_EXCEEDED"
			entry.Row.Attempts = []Attempt{
				{Account: "a@example.com", URL: "https://a/v1internal:streamGenerateContent", Status: 503, DurationMS: 120},
				{Account: "b@example.com", URL: "https://a/v1internal:streamGenerateContent", Status: 429, Code: "RATE_LIMIT_EXCEEDED", RetryDelay: "3.5s", TraceID: "t-1", DurationMS: 80},
			}
			entry.Upstream = []Body{NewBody([]byte(`{"n":0}`), []byte("busy")), NewBody([]byte(`{"n":1}`), []byte(`{"error":{"code":429}}`))}
		}
		if index == 0 {
			body := NewBody([]byte(`{"stream":true}`), []byte(strings.Repeat("x", BodyLimit+10)))
			entry.Body = &body
		}
		store.Record(entry)
	}
	store = reopen(t, store, path)
	defer store.Close()
	ctx := context.Background()
	query := RecordQuery{From: now.Add(-time.Hour), To: now.Add(time.Second), Limit: 2}
	var ids []string
	for {
		items, next, err := store.Records(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range items {
			ids = append(ids, row.ID)
		}
		if next == nil {
			break
		}
		cursor, err := DecodeCursor(next.Encode())
		if err != nil {
			t.Fatal(err)
		}
		query.Before = &cursor
	}
	if strings.Join(ids, ",") != "req_0,req_1,req_2,req_3,req_4" {
		t.Fatalf("分页顺序 %v", ids)
	}
	for name, query := range map[string]RecordQuery{
		"额度组":      {Filters: map[string][]string{"quota_group": {"3p"}}},
		"账户与结果":    {Filters: map[string][]string{"account": {"b@example.com", "c@example.com"}, "state": {StateFailed}}},
		"状态码":      {Status: 429},
		"错误关键字":    {Search: "HYPERLINK"},
		"客户端请求 ID": {Search: "client-3"},
		"响应 ID":    {Search: "chatcmpl_3"},
		"cascade":  {Search: "cascade-3"},
	} {
		query.From, query.To, query.Limit = now.Add(-time.Hour), now.Add(time.Second), 10
		items, _, err := store.Records(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || items[0].ID != "req_3" {
			t.Fatalf("%s: %+v", name, items)
		}
	}
	detail, err := store.Detail(ctx, "req_0")
	if err != nil {
		t.Fatal(err)
	}
	row, body := detail.Record, detail.Body
	if row.DurationMS != 1500 || row.FirstEventMS != 300 || !row.Stream || row.CachedTokens != 4 || row.ResponseID != "chatcmpl_0" || row.RequestType != "agent" || row.PromptTier != "full" {
		t.Fatalf("row=%+v", row)
	}
	if body == nil || body.Request != `{"stream":true}` || len(body.Response) != BodyLimit || body.ResponseSize != BodyLimit+10 || len(detail.Upstream) != 0 {
		t.Fatalf("body=%v upstream=%v", body != nil, detail.Upstream)
	}
	detail, err = store.Detail(ctx, "req_3")
	if err != nil || detail.Body != nil || len(detail.Upstream) != 2 || detail.Upstream[1].Response != `{"error":{"code":429}}` || detail.Upstream[0].Request != `{"n":0}` {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	if attempts := detail.Record.Attempts; len(attempts) != 2 || attempts[1].RetryDelay != "3.5s" || attempts[1].TraceID != "t-1" || attempts[0].URL == "" || detail.Record.ErrorCode != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("attempts=%+v", detail.Record)
	}
	if _, err := store.Detail(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("缺失记录 err=%v", err)
	}
	if _, err := DecodeCursor("bm90LWEtY3Vyc29y"); err == nil {
		t.Fatal("无效游标被接受")
	}
	var output bytes.Buffer
	if err := store.ExportRecords(ctx, RecordQuery{From: now.Add(-time.Hour), To: now.Add(time.Second), Status: 429}, &output); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte("\xef\xbb\xbf")) {
		t.Fatal("CSV 缺少 BOM")
	}
	records, err := csv.NewReader(bytes.NewReader(output.Bytes()[3:])).ReadAll()
	if err != nil || len(records) != 2 || len(records[0]) != len(records[1]) || records[0][0] != "id" || records[1][0] != "req_3" {
		t.Fatalf("csv=%v err=%v", records, err)
	}
	if got := records[1][len(records[1])-2]; got != "'=HYPERLINK(1)" {
		t.Fatalf("公式文本未转义: %q", got)
	}
}

// TestBodyRetention 验证上游正文只保留最近 1000 条
func TestBodyRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	store := openStore(t, path)
	now := time.Now().UTC()
	for index := range bodyRetention + 2 {
		store.Record(Entry{Row: Row{ID: fmt.Sprintf("r%d", index), Time: now, State: StateFailed}, Upstream: []Body{NewBody([]byte("in"), []byte("out"))}})
	}
	store = reopen(t, store, path)
	defer store.Close()
	if detail, err := store.Detail(context.Background(), "r1"); err != nil || len(detail.Upstream) != 0 {
		t.Fatalf("超出保留条数的上游正文仍存在: %v %v", detail.Upstream, err)
	}
	if detail, err := store.Detail(context.Background(), fmt.Sprintf("r%d", bodyRetention+1)); err != nil || len(detail.Upstream) != 1 {
		t.Fatalf("最近的上游正文缺失: %v", err)
	}
}

// TestRetentionPrunesExpiredData 验证超过 90 天的记录、汇总、额度快照与日志在打开账本时被删除
func TestRetentionPrunesExpiredData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	store := openStore(t, path)
	now := time.Now().UTC().Truncate(time.Millisecond)
	old := now.Add(-Retention - 48*time.Hour)
	store.Record(Entry{Row: Row{ID: "old", Time: old, State: StateCompleted}})
	store.Record(Entry{Row: Row{ID: "new", Time: now, State: StateCompleted}})
	store.RecordLog(Log{Time: old, Level: "WARN", Message: "old"})
	store.RecordLog(Log{Time: now, Level: "WARN", Message: "new"})
	store = reopen(t, store, path)
	store.db.Exec(`UPDATE quota_snapshots SET time = ?`, old.UnixMilli())
	store.RecordQuota("a@example.com", []antigravity.QuotaGroup{{Buckets: []antigravity.QuotaBucket{
		{BucketID: "gemini-5h", Window: "5h", RemainingFraction: 0.75, ResetTime: "2026-10-14T20:00:00Z"},
		{BucketID: "gemini-weekly", Window: "weekly", RemainingFraction: 0},
	}}})
	store = reopen(t, store, path)
	store.db.Exec(`UPDATE quota_snapshots SET time = ? WHERE bucket_id = 'gemini-weekly'`, old.UnixMilli())
	store = reopen(t, store, path)
	defer store.Close()
	ctx := context.Background()
	items, _, err := store.Records(ctx, RecordQuery{From: old.Add(-time.Hour), To: now.Add(time.Second), Limit: 10})
	if err != nil || len(items) != 1 || items[0].ID != "new" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	var hours int
	store.db.QueryRow(`SELECT COUNT(*) FROM hours WHERE start < ?`, now.Add(-Retention).UnixMilli()).Scan(&hours)
	if hours != 0 {
		t.Fatalf("过期小时汇总 %d", hours)
	}
	logs, _, err := store.Logs(ctx, LogQuery{From: old.Add(-time.Hour), To: now.Add(time.Second), Limit: 10})
	if err != nil || len(logs) != 1 || logs[0].Message != "new" {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
	snapshots, err := store.QuotaSnapshots(ctx, old.Add(-time.Hour), time.Now().Add(time.Second), "a@example.com")
	if err != nil || len(snapshots) != 1 || snapshots[0].BucketID != "gemini-5h" || snapshots[0].Window != "5h" ||
		snapshots[0].ResetTime == nil || !snapshots[0].ResetTime.Equal(time.Date(2026, 10, 14, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("snapshots=%+v err=%v", snapshots, err)
	}
}

// TestUsageMatchesRecords 验证跨日与跨小时的报告由日、小时汇总与原始记录拼成后与逐条累加一致，分位数误差在 1% 内
func TestUsageMatchesRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	store := openStore(t, path)
	end := time.Now().UTC().Truncate(time.Hour)
	start := end.Add(-50 * time.Hour)
	var durations []int64
	inputs, unpriced, cost := int64(0), int64(0), 0.0
	upstream := []string{"claude-opus-5-5-high", "gemini-2.5-flash", "gpt-6-astra"}
	for index := range 300 {
		duration := int64(100 + index*37)
		row := Row{
			ID: fmt.Sprintf("u%d", index), Time: start.Add(time.Duration(index) * 10 * time.Minute), Protocol: "anthropic",
			Model: []string{"gemini-pro", "claude-opus"}[index%2], Account: "a@example.com", APIKey: "sk-…abcd",
			QuotaGroup: []string{"gemini", "3p"}[index%2], Status: 200, State: StateCompleted, DurationMS: duration, FirstEventMS: 50,
			InputTokens: 10, CachedTokens: 2, ReasoningTokens: 3, ReplyTokens: 5,
		}
		if index%10 == 0 {
			row.State, row.Status = StateFailed, 429
		} else {
			durations = append(durations, duration)
		}
		row.UpstreamModel = upstream[index%3]
		if value, ok := Cost(row.UpstreamModel, 10, 2, 8); ok {
			cost += value
		} else {
			unpriced++
		}
		inputs += row.InputTokens
		store.Record(Entry{Row: row})
	}
	store = reopen(t, store, path)
	defer store.Close()
	report, err := store.Usage(context.Background(), UsageQuery{From: start, To: end, Bucket: time.Hour, Stack: "model"})
	if err != nil {
		t.Fatal(err)
	}
	totals := report.Totals
	if totals.Requests != 300 || totals.Failed != 30 || totals.RateLimited != 30 || totals.InputTokens != inputs || totals.TotalTokens != 300*18 {
		t.Fatalf("totals=%+v", totals)
	}
	if unpriced != 100 || totals.Unpriced != unpriced || math.Abs(totals.CostUSD-cost) > 1e-9 {
		t.Fatalf("cost=%v unpriced=%d 期望 cost=%v unpriced=%d", totals.CostUSD, totals.Unpriced, cost, unpriced)
	}
	page, _, err := store.Records(context.Background(), RecordQuery{From: start, To: end, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range page {
		if priced := row.UpstreamModel != "gpt-6-astra"; priced != (row.CostUSD != nil) {
			t.Fatalf("%s cost_usd=%v", row.UpstreamModel, row.CostUSD)
		}
	}
	if len(report.Buckets) != 50 || len(report.Series) != 2 || len(report.Groups["quota_group"]) != 2 || len(report.Groups["status"]) != 2 {
		t.Fatalf("buckets=%d series=%d groups=%v", len(report.Buckets), len(report.Series), report.Groups["status"])
	}
	bucketRequests := int64(0)
	for _, bucket := range report.Buckets {
		bucketRequests += bucket.Requests
	}
	if bucketRequests != 300 {
		t.Fatalf("分桶合计 %d", bucketRequests)
	}
	exact := durations[int(math.Ceil(0.95*float64(len(durations))))-1]
	if math.Abs(totals.Duration.P95MS-float64(exact))/float64(exact) > 0.01 {
		t.Fatalf("p95=%v 精确值=%d", totals.Duration.P95MS, exact)
	}
	filtered, err := store.Usage(context.Background(), UsageQuery{From: start, To: end, Bucket: 24 * time.Hour, Stack: "account", Filters: map[string][]string{"quota_group": {"3p"}}})
	if err != nil || filtered.Totals.Requests != 150 || len(filtered.Options["model"]) != 2 {
		t.Fatalf("筛选报告 %+v err=%v", filtered.Totals, err)
	}
}

// TestLogsQuery 验证日志按请求 ID、来源、模型、级别与关键字检索，按序号分页，并可清空
func TestLogsQuery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requests.db")
	store := openStore(t, path)
	now := time.Now().UTC().Truncate(time.Millisecond)
	for index := range 5 {
		store.RecordLog(Log{
			Time: now.Add(time.Duration(index) * time.Second), Level: []string{"WARN", "ERROR"}[index%2], Source: "a@example.com",
			Event: "request.progress", Message: fmt.Sprintf("上游尝试失败 %d", index), RequestID: fmt.Sprintf("req_%d", index%2), Model: "gemini-pro",
			Attrs: map[string]any{"code": "RATE_LIMIT_EXCEEDED"}, Request: &Row{ID: "req_1", State: StateFailed},
		})
	}
	store = reopen(t, store, path)
	defer store.Close()
	ctx := context.Background()
	window := LogQuery{From: now.Add(-time.Minute), To: now.Add(time.Minute), Limit: 10}
	for name, test := range map[string]struct {
		query LogQuery
		count int
	}{
		"请求 ID": {LogQuery{RequestID: "req_1"}, 2},
		"错误级别":  {LogQuery{Level: "ERROR"}, 2},
		"来源与模型": {LogQuery{Source: "a@example.com", Model: "gemini-pro"}, 5},
		"属性关键字": {LogQuery{Search: "RATE_LIMIT"}, 5},
		"消息关键字": {LogQuery{Search: "失败 3"}, 1},
	} {
		query := test.query
		query.From, query.To, query.Limit = window.From, window.To, window.Limit
		items, _, err := store.Logs(ctx, query)
		if err != nil || len(items) != test.count {
			t.Fatalf("%s: %d 条 err=%v", name, len(items), err)
		}
	}
	window.Limit = 3
	first, next, err := store.Logs(ctx, window)
	if err != nil || len(first) != 3 || next == 0 || first[0].Message != "上游尝试失败 4" || first[0].Request == nil || first[0].Attrs["code"] != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("first=%+v next=%d err=%v", first, next, err)
	}
	window.Before = next
	second, next, err := store.Logs(ctx, window)
	if err != nil || len(second) != 2 || next != 0 {
		t.Fatalf("second=%+v next=%d err=%v", second, next, err)
	}
	if err := store.ClearLogs(ctx); err != nil {
		t.Fatal(err)
	}
	if items, _, _ := store.Logs(ctx, LogQuery{From: window.From, To: window.To, Limit: 10}); len(items) != 0 {
		t.Fatalf("清空后仍有 %d 条", len(items))
	}
}

// TestTraceOutcome 验证生成事件、响应状态码与取消原因对结果、状态码、错误、错误阶段、用量与上游正文的判定
func TestTraceOutcome(t *testing.T) {
	usage := antigravity.Event{Kind: antigravity.EventUsage, Usage: &antigravity.Usage{InputTokens: 100, CachedTokens: 40, OutputTokens: 30, ReasoningTokens: 20, TotalTokens: 150}}
	finish := func(reason string) antigravity.Event {
		return antigravity.Event{Kind: antigravity.EventFinish, FinishReason: reason, ProviderModel: "claude-opus-5-5"}
	}
	text := antigravity.Event{Kind: antigravity.EventText, Text: "hi"}
	tool := antigravity.Event{Kind: antigravity.EventToolCall, ToolCall: &antigravity.FunctionCall{Name: "echo"}}
	streamError := antigravity.Event{Kind: antigravity.EventError, Err: &antigravity.UpstreamError{Status: 429, Body: []byte("exhausted")}}
	ok := Attempt{Account: "a@example.com", URL: "https://upstream/v1internal:streamGenerateContent", Status: 200, Request: []byte("envelope"), Response: []byte("data: {}")}
	limited := Attempt{Account: "a@example.com", Status: 429, Response: []byte(`{"error":{"code":429,"message":"Resource has been exhausted","status":"RESOURCE_EXHAUSTED",
"details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"RATE_LIMIT_EXCEEDED"},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"3.5s"}]}}`)}
	refused := Attempt{Account: "a@example.com", Status: 0, Error: "connection reset"}
	cases := []struct {
		name     string
		begin    bool
		attempts []Attempt
		events   []antigravity.Event
		fail     error
		status   int
		cause    error
		message  string
		capture  bool
		state    string
		code     int
		errText  string
		phase    string
		tokens   int64
		bodies   int
	}{
		{name: "完成", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, usage, finish("STOP")}, status: 200, state: StateCompleted, code: 200, tokens: 100},
		{name: "完成并保存正文", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, usage, finish("STOP")}, status: 200, capture: true, state: StateCompleted, code: 200, tokens: 100, bodies: 1},
		{name: "工具调用", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{tool, usage, finish("STOP")}, status: 200, state: StateToolCalls, code: 200, tokens: 100},
		{name: "输出上限", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, usage, finish("MAX_TOKENS")}, status: 200, state: StateLimited, code: 200, tokens: 100},
		{name: "上游终止", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, finish("SAFETY")}, status: 200, state: StateBlocked, code: 200, errText: "SAFETY"},
		{name: "流中错误", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, streamError}, status: 200, state: StateFailed, code: 429, errText: "upstream 429: exhausted", phase: "upstream", bodies: 1},
		{name: "换号后仍失败", begin: true, attempts: []Attempt{limited, limited}, fail: &antigravity.UpstreamError{Status: 429, Body: []byte("exhausted")}, status: 429, state: StateFailed, code: 429, errText: "upstream 429: exhausted", phase: "upstream", bodies: 2},
		{name: "连接失败", begin: true, attempts: []Attempt{refused}, fail: errors.New("connection reset"), status: 502, state: StateFailed, code: 502, errText: "connection reset", phase: "network", bodies: 1},
		{name: "无可用账户", begin: true, fail: errors.New("没有可调度的账户"), status: 503, state: StateFailed, code: 503, errText: "没有可调度的账户", phase: "routing"},
		{name: "请求无效", status: 400, message: "messages 不能为空", state: StateFailed, code: 400, errText: "messages 不能为空", phase: "request"},
		{name: "无错误说明", status: 503, state: StateFailed, code: 503, errText: http.StatusText(503), phase: "internal"},
		{name: "中途断开", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, usage}, status: 200, cause: context.Canceled, state: StateCancelled, code: StatusClientClosed, errText: "客户端断开连接", tokens: 100},
		{name: "终止事件后断开", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text, usage, finish("STOP")}, status: 200, cause: context.Canceled, state: StateCompleted, code: 200, tokens: 100},
		{name: "管理端停止", begin: true, events: []antigravity.Event{text}, status: 200, cause: ErrCancelledByAdmin, state: StateCancelled, code: StatusClientClosed, errText: ErrCancelledByAdmin.Error()},
		{name: "超时", begin: true, attempts: []Attempt{ok}, events: []antigravity.Event{text}, status: 200, cause: ErrRequestTimeout, state: StateFailed, code: http.StatusGatewayTimeout, errText: ErrRequestTimeout.Error(), phase: "timeout", bodies: 1},
		{name: "取消错误", begin: true, fail: context.Canceled, status: 503, state: StateCancelled, code: StatusClientClosed, errText: "客户端断开连接"},
	}
	for _, test := range cases {
		started := time.Now()
		trace := NewTrace(started, Row{ID: "req_1", Path: "/v1/messages"})
		if test.begin && (!trace.Begin("msg_1", "claude-opus-5-5-high") || trace.Begin("msg_2", "other")) {
			t.Errorf("%s: 重复生成时 Begin 返回值错误", test.name)
		}
		trace.SetRequest(true, "body-model")
		trace.SetAccount("a@example.com")
		trace.SetUpstream("claude-opus-5-5-high", "3p", "agent")
		trace.SetConversation("cascade-1", 3)
		for _, attempt := range test.attempts {
			trace.AddAttempt(attempt)
		}
		if test.fail != nil {
			trace.Fail(test.fail)
		}
		for _, event := range test.events {
			trace.Observe(event)
		}
		entry := trace.Finish(started.Add(2*time.Second), test.status, test.cause, test.message, test.capture)
		row := entry.Row
		if row.ID != "req_1" || !row.Stream || row.Account != "a@example.com" || row.QuotaGroup != "3p" || row.DurationMS != 2000 ||
			row.RequestType != "agent" || row.CascadeID != "cascade-1" || row.StepIndex != 3 {
			t.Errorf("%s: 标识字段 %+v", test.name, row)
		}
		if test.begin && (row.ResponseID != "msg_1" || row.Model != "claude-opus-5-5-high") {
			t.Errorf("%s: 生成字段 %+v", test.name, row)
		}
		if row.State != test.state || row.Status != test.code || row.Error != test.errText || row.ErrorPhase != test.phase || row.InputTokens != test.tokens || len(entry.Upstream) != test.bodies {
			t.Errorf("%s: state=%s status=%d error=%q phase=%q tokens=%d bodies=%d", test.name, row.State, row.Status, row.Error, row.ErrorPhase, row.InputTokens, len(entry.Upstream))
		}
	}
	trace := NewTrace(time.Now(), Row{ID: "req_2"})
	trace.AddAttempt(limited)
	row := trace.Finish(time.Now(), 429, nil, "", false).Row
	if attempt := row.Attempts[0]; attempt.Code != "RATE_LIMIT_EXCEEDED" || attempt.RetryDelay != "3.5s" || attempt.Error != "Resource has been exhausted" ||
		row.UpstreamStatus != 429 || row.ErrorCode != "RATE_LIMIT_EXCEEDED" {
		t.Fatalf("上游错误解析 %+v", row)
	}
	trace = NewTrace(time.Now().Add(-time.Second), Row{ID: "req_3"})
	trace.SetRequest(false, "body-model")
	for _, event := range []antigravity.Event{text, usage, tool, usage, finish("STOP")} {
		trace.Observe(event)
	}
	row = trace.Finish(time.Now(), 200, nil, "", false).Row
	if row.InputTokens != 200 || row.CachedTokens != 80 || row.ReasoningTokens != 40 || row.ReplyTokens != 60 || row.State != StateToolCalls ||
		row.ToolCalls != 1 || row.ModelVersion != "claude-opus-5-5" || row.Model != "body-model" || row.FirstEventMS < 1000 {
		t.Fatalf("用量与诊断字段 %+v", row)
	}
	var missing *Trace
	missing.SetAccount("a")
	missing.SetUpstream("m", "g", "agent")
	missing.SetConversation("c", 1)
	missing.AddAttempt(Attempt{})
	if TraceFrom(context.Background()) != nil || TraceFrom(WithTrace(context.Background(), trace)) != trace {
		t.Fatal("context 中的 Trace 不一致")
	}
}

// TestUpstreamErrorFormats 验证数组形式的错误体与只有 quotaResetDelay 的额度耗尽错误
func TestUpstreamErrorFormats(t *testing.T) {
	code, delay, message := upstreamError([]byte(`[{"error":{"code":429,"message":"quota","status":"RESOURCE_EXHAUSTED","details":[
{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"QUOTA_EXHAUSTED","metadata":{"quotaResetDelay":"1h2m3s"}}]}}]`))
	if code != "QUOTA_EXHAUSTED" || delay != "1h2m3s" || message != "quota" {
		t.Fatalf("code=%s delay=%s message=%s", code, delay, message)
	}
	if code, delay, message := upstreamError([]byte("<html>bad gateway</html>")); code != "" || delay != "" || message != "" {
		t.Fatalf("非 JSON 错误体 %s %s %s", code, delay, message)
	}
}

func TestCost(t *testing.T) {
	for _, test := range []struct {
		model                 string
		input, cached, output int64
		want                  float64
		priced                bool
	}{
		{"claude-opus-5-5-high", 1_000_000, 0, 1_000_000, 24, true},
		{"claude-sonnet-5-5", 1_000_000, 1_000_000, 0, 0.10, true},
		{"gemini-3.8-flash-tiered", 2_000_000, 1_000_000, 0, 0.825, true},
		{"gemini-3.5-flash-extra-low", 0, 0, 1_000_000, 9, true},
		{"gemini-2.5-pro", 1_000_000, 0, 1_000_000, 17.5, true},
		{"gemini-2.5-pro", 100_000, 0, 0, 0.125, true},
		{"claude-opus-4-6-thinking", 1_000_000, 1_000_000, 0, 0.50, true},
		{"gpt-6-astra", 1_000_000, 0, 0, 0, false},
	} {
		got, priced := Cost(test.model, test.input, test.cached, test.output)
		if priced != test.priced || math.Abs(got-test.want) > 1e-9 {
			t.Errorf("%s=%v,%v 期望 %v,%v", test.model, got, priced, test.want, test.priced)
		}
	}
	if value := rowCost(&Row{UpstreamModel: ""}); value == nil || *value != 0 {
		t.Fatalf("没有 token 的请求 cost=%v", value)
	}
	if value := rowCost(&Row{UpstreamModel: "gpt-6-astra", ReplyTokens: 1}); value != nil {
		t.Fatalf("没有单价的请求 cost=%v", *value)
	}
}
