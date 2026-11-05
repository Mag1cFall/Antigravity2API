package app

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/logging"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// warmupResult 是一次手动预热的结果，Request 是请求账本中的记录 ID，Before 与 After 是预热前后该模型额度组的额度桶
type warmupResult struct {
	Account    string        `json:"account"`
	Model      string        `json:"model"`
	QuotaGroup string        `json:"quota_group,omitempty"`
	OK         bool          `json:"ok"`
	DurationMS int64         `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
	Request    string        `json:"request"`
	Before     []quotaBucket `json:"before"`
	After      []quotaBucket `json:"after"`
}

// postWarmup 以选定的账户与模型经生成路径发送一次最小请求，登记为可从管理端停止的进行中请求，写入请求账本并返回结果与预热前后该模型额度组的额度桶，管理端断开连接时预热照常完成
func (s *server) postWarmup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Account string `json:"account"`
		Model   string `json:"model"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	id := logging.RequestID(r.Context())
	trace := requestdb.NewTrace(time.Now(), requestdb.Row{
		ID: id, Protocol: "warmup", Path: r.URL.Path, ClientIP: clientIP(r), UserAgent: r.UserAgent(),
		PromptTier: s.live.Load().SystemPrompt, Account: input.Account,
	})
	trace.Watch(s.events.begin)
	trace.Begin("", input.Model)
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(r.Context()))
	defer cancel(nil)
	s.events.track(id, &activeRequest{trace: trace, cancel: cancel})
	s.events.begin(trace.Snapshot())
	before, _ := s.findAccount(input.Account)
	group, err := s.deps.Service.Prewarm(requestdb.WithTrace(ctx, trace), input.Account, input.Model)
	trace.Fail(err)
	entry := trace.Finish(time.Now(), http.StatusOK, context.Cause(ctx), "", s.ledger.BodyCapture())
	s.ledger.Record(entry)
	s.events.end(id, entry.Row)
	after, _ := s.findAccount(input.Account)
	result := warmupResult{
		Account: input.Account, Model: input.Model, QuotaGroup: group, OK: err == nil, DurationMS: entry.Row.DurationMS,
		Request: id, Before: groupBuckets(before.Quota, group), After: groupBuckets(after.Quota, group),
	}
	level, text := slog.LevelInfo, fmt.Sprintf("手动预热完成 | 模型=%s | 耗时=%dms", input.Model, result.DurationMS)
	if err != nil {
		result.Error = err.Error()
		level, text = slog.LevelWarn, fmt.Sprintf("手动预热失败 | 模型=%s | 耗时=%dms | 错误=%s", input.Model, result.DurationMS, result.Error)
	}
	slog.Log(r.Context(), level, text, "source", input.Account)
	writeJSON(w, http.StatusOK, result)
}

// groupBuckets 返回属于额度组的额度桶
func groupBuckets(buckets []quotaBucket, group string) []quotaBucket {
	matched := []quotaBucket{}
	for _, bucket := range buckets {
		if antigravity.BucketGroup(bucket.BucketID) == group {
			matched = append(matched, bucket)
		}
	}
	return matched
}
