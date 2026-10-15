package app

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Mag1cFall/Antigravity2API/internal/antigravity"
	"github.com/Mag1cFall/Antigravity2API/internal/auth"
	"github.com/Mag1cFall/Antigravity2API/internal/config"
	"github.com/Mag1cFall/Antigravity2API/internal/requestdb"
)

// maxBuckets 是一次用量查询允许的最多分桶数
const maxBuckets = 1500

// bucketSizes 是允许手动选择的用量分桶时长
var bucketSizes = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 24 * time.Hour}

// admin 注册管理 API
func (s *server) admin() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/session", s.getSession)
	mux.HandleFunc("POST /api/session", s.login)
	mux.HandleFunc("DELETE /api/session", s.logout)
	mux.HandleFunc("GET /api/status", s.getStatus)
	mux.HandleFunc("GET /api/accounts", s.listAccounts)
	mux.HandleFunc("PATCH /api/accounts/{id}", s.updateAccount)
	mux.HandleFunc("DELETE /api/accounts/{id}", s.deleteAccount)
	mux.HandleFunc("POST /api/accounts/{id}/verify", s.verifyAccount)
	mux.HandleFunc("POST /api/accounts/{id}/quota", s.refreshQuota)
	mux.HandleFunc("POST /api/accounts/{id}/switch", s.switchAccount)
	mux.HandleFunc("POST /api/accounts/import", s.importAccounts)
	mux.HandleFunc("POST /api/accounts/refresh", s.refreshAccounts)
	mux.HandleFunc("POST /api/logins", s.createLogin)
	mux.HandleFunc("POST /api/logins/{id}/code", s.submitLoginCode)
	mux.HandleFunc("DELETE /api/logins/{id}", s.cancelLogin)
	mux.HandleFunc("POST /api/pairing", s.createPairing)
	mux.HandleFunc("GET /api/models", s.listModels)
	mux.HandleFunc("GET /api/quota/snapshots", s.listQuotaSnapshots)
	mux.HandleFunc("GET /api/usage", s.getUsage)
	mux.HandleFunc("GET /api/usage/records", s.listRecords)
	mux.HandleFunc("GET /api/usage/records.csv", s.exportRecords)
	mux.HandleFunc("GET /api/usage/records/{id}", s.getRecord)
	mux.HandleFunc("GET /api/requests", s.listActive)
	mux.HandleFunc("POST /api/requests/{id}/cancel", s.cancelRequest)
	mux.HandleFunc("GET /api/logs", s.listLogs)
	mux.HandleFunc("DELETE /api/logs", s.clearLogs)
	mux.HandleFunc("GET /api/prompt/preview", s.getPromptPreview)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PATCH /api/settings", s.updateSettings)
	mux.HandleFunc("GET /api/events", s.streamEvents)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "管理 API 不存在: "+r.Method+" "+r.URL.Path)
	})
	return mux
}

// getStatus 返回宿主、版本、监听地址、启动时间、模拟的官方客户端版本与官方更新清单中的最新版本，尚未读到更新清单时最新版本为空
func (s *server) getStatus(w http.ResponseWriter, _ *http.Request) {
	version := "(devel)"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		version = info.Main.Version
	}
	latest := ""
	if value := s.latest.Load(); value != nil {
		latest = *value
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"host": s.deps.Host, "version": version, "listen": s.boot.Listen, "started_at": s.started,
		"client_version": antigravity.ClientVersion, "latest_version": latest,
	})
}

// listAccounts 返回全部账户
func (s *server) listAccounts(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"accounts": s.accounts()})
}

// findAccount 返回管理端账户，不存在时返回 false
func (s *server) findAccount(id string) (account, bool) {
	for _, item := range s.accounts() {
		if item.ID == id {
			return item, true
		}
	}
	return account{}, false
}

// stored 返回账户目录中的账户，不存在时返回 nil
func (s *server) stored(id string) *auth.Account {
	for _, account := range s.deps.Store.Accounts() {
		if account.ID == id {
			return account
		}
	}
	return nil
}

// updateAccount 启用或停用账户、设置账户代理与调度优先级并在后台刷新账户池，代理变化的账户在刷新时重新初始化
func (s *server) updateAccount(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Enabled *bool `json:"enabled"`
		// Proxy 为空串时跟随全局 PROXY
		Proxy *string `json:"proxy"`
		// Priority 为调度优先级，越大越先使用
		Priority *int `json:"priority"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	stored := s.stored(r.PathValue("id"))
	if stored == nil {
		writeError(w, http.StatusNotFound, "account_not_found", "账户不存在: "+r.PathValue("id"))
		return
	}
	if input.Proxy != nil {
		proxy := strings.TrimSpace(*input.Proxy)
		if err := config.ValidateProxy(proxy); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_proxy", err.Error())
			return
		}
		stored.Proxy = proxy
	}
	if input.Enabled != nil {
		stored.Enabled = *input.Enabled
	}
	if input.Priority != nil {
		stored.Priority = *input.Priority
	}
	if err := s.deps.Store.Save(stored); err != nil {
		writeError(w, http.StatusInternalServerError, "account_update_failed", err.Error())
		return
	}
	if input.Enabled != nil {
		action := "停用"
		if *input.Enabled {
			action = "启用"
		}
		slog.InfoContext(r.Context(), "账户已"+action, "source", stored.ID)
	}
	if input.Proxy != nil {
		slog.InfoContext(r.Context(), "账户代理已设置 | 代理="+cmp.Or(redactProxy(stored.Proxy), "跟随全局"), "source", stored.ID)
	}
	if input.Priority != nil {
		slog.InfoContext(r.Context(), "账户优先级已设置 | 优先级="+strconv.Itoa(stored.Priority), "source", stored.ID)
	}
	s.refreshLater()
	item, _ := s.findAccount(stored.ID)
	writeJSON(w, http.StatusOK, item)
}

// deleteAccount 删除账户并在后台刷新账户池
func (s *server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.stored(id) == nil {
		writeError(w, http.StatusNotFound, "account_not_found", "账户不存在: "+id)
		return
	}
	if err := s.deps.Store.Remove(id); err != nil {
		writeError(w, http.StatusInternalServerError, "account_delete_failed", err.Error())
		return
	}
	slog.InfoContext(r.Context(), "账户已删除 | 账户="+id, "source", "auth")
	s.refreshLater()
	w.WriteHeader(http.StatusNoContent)
}

// verifyAccount 重新初始化 error 状态的账户并返回验证后的账户，其他状态返回 409，初始化失败时返回 502 与上游原因
func (s *server) verifyAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, ok := s.findAccount(id)
	if !ok {
		writeError(w, http.StatusNotFound, "account_not_found", "账户不存在: "+id)
		return
	}
	if current.State != "error" {
		writeError(w, http.StatusConflict, "account_not_verifiable", fmt.Sprintf("账户状态为 %s，只有 error 状态的账户需要验证", current.State))
		return
	}
	started := time.Now()
	if err := s.deps.Service.Verify(r.Context(), id); err != nil {
		slog.ErrorContext(r.Context(), fmt.Sprintf("账户验证失败 | 耗时=%s | 错误=%v", time.Since(started).Round(time.Millisecond), err), "source", id)
		writeError(w, http.StatusBadGateway, "account_verify_failed", err.Error())
		return
	}
	slog.InfoContext(r.Context(), fmt.Sprintf("账户验证完成 | 耗时=%s", time.Since(started).Round(time.Millisecond)), "source", id)
	item, _ := s.findAccount(id)
	writeJSON(w, http.StatusOK, item)
}

// refreshQuota 实时读取一个账户的额度并返回该账户
func (s *server) refreshQuota(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.findAccount(id); !ok {
		writeError(w, http.StatusNotFound, "account_not_found", "账户不存在: "+id)
		return
	}
	if err := s.deps.Service.RefreshQuota(r.Context(), id); err != nil {
		slog.WarnContext(r.Context(), "读取额度摘要失败 | 错误="+err.Error(), "source", id)
		writeError(w, http.StatusBadGateway, "quota_refresh_failed", err.Error())
		return
	}
	item, _ := s.findAccount(id)
	writeJSON(w, http.StatusOK, item)
}

// switchAccount 把账户写入本机官方客户端并重新启动官方客户端
func (s *server) switchAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.stored(id) == nil {
		writeError(w, http.StatusNotFound, "account_not_found", "账户不存在: "+id)
		return
	}
	if err := auth.SwitchOfficial(s.deps.Store, id); err != nil {
		slog.ErrorContext(r.Context(), "官方客户端切换账户失败 | 错误="+err.Error(), "source", id)
		writeError(w, http.StatusBadGateway, "switch_failed", err.Error())
		return
	}
	slog.InfoContext(r.Context(), "官方客户端已切换到该账户并重新启动", "source", id)
	writeJSON(w, http.StatusOK, map[string]any{"account": id})
}

// importAccounts 从本机官方客户端导入账户并在后台刷新账户池
func (s *server) importAccounts(w http.ResponseWriter, r *http.Request) {
	imported, err := auth.ImportOfficial(r.Context(), s.deps.Store)
	if err != nil {
		slog.ErrorContext(r.Context(), "官方客户端账户导入失败 | 错误="+err.Error(), "source", "auth")
		writeError(w, http.StatusBadGateway, "import_failed", err.Error())
		return
	}
	ids := make([]string, 0, len(imported))
	for _, account := range imported {
		ids = append(ids, account.ID)
	}
	slog.InfoContext(r.Context(), "官方客户端账户导入完成 | 账户="+strings.Join(ids, ","), "source", "auth")
	s.refreshLater()
	writeJSON(w, http.StatusOK, map[string]any{"accounts": ids})
}

// refreshAccounts 同步刷新账户池并读取全部账户的额度，返回刷新后的账户与读取额度失败的账户及原因
func (s *server) refreshAccounts(w http.ResponseWriter, r *http.Request) {
	s.refresh(r.Context())
	failed := map[string]string{}
	for id, err := range s.readQuotas(r.Context()) {
		failed[id] = err.Error()
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": s.accounts(), "failed": failed})
}

// adminModel 是管理端模型条目，Accounts 为可调度到该模型的就绪账户数
type adminModel struct {
	antigravity.Model
	Accounts int `json:"accounts"`
}

// listModels 返回各就绪账户模型目录的合集
func (s *server) listModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.deps.Service.Models(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "models_failed", err.Error())
		return
	}
	counts := s.deps.Service.ModelAccounts()
	listed := make([]adminModel, len(models))
	for i, model := range models {
		listed[i] = adminModel{Model: model, Accounts: counts[model.ID]}
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": listed})
}

// listQuotaSnapshots 返回范围内的额度快照
func (s *server) listQuotaSnapshots(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	from, to, err := parseRange(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	snapshots, err := s.ledger.QuotaSnapshots(r.Context(), from, to, values.Get("account"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "quota_snapshots_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"snapshots": snapshots})
}

// getUsage 返回用量报告；bucket 为 auto 或秒数，tz 为分桶对齐的 IANA 时区，stack 为堆叠维度
func (s *server) getUsage(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	from, to, err := parseRange(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	query := requestdb.UsageQuery{From: from, To: to, Location: time.UTC, Filters: filters(values), Stack: "model"}
	if name := values.Get("tz"); name != "" {
		if query.Location, err = time.LoadLocation(name); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "tz 必须是 IANA 时区名")
			return
		}
	}
	query.Bucket = autoBucket(to.Sub(from))
	if value := values.Get("bucket"); value != "" && value != "auto" {
		seconds, err := strconv.Atoi(value)
		query.Bucket = time.Duration(seconds) * time.Second
		if err != nil || !slices.Contains(bucketSizes, query.Bucket) {
			writeError(w, http.StatusBadRequest, "invalid_request", "bucket 必须是 auto、60、300、900、3600 或 86400")
			return
		}
	}
	if to.Sub(from)/query.Bucket > maxBuckets {
		writeError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("分桶数超过 %d，请选择更大的粒度", maxBuckets))
		return
	}
	if value := values.Get("stack"); value != "" {
		if !slices.Contains(requestdb.Dimensions, value) {
			writeError(w, http.StatusBadRequest, "invalid_request", "stack 必须是 "+strings.Join(requestdb.Dimensions, "、"))
			return
		}
		query.Stack = value
	}
	report, err := s.ledger.Usage(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "usage_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// autoBucket 按时间范围长度选择分桶时长
func autoBucket(span time.Duration) time.Duration {
	switch {
	case span <= 2*time.Hour:
		return time.Minute
	case span <= 12*time.Hour:
		return 5 * time.Minute
	case span <= 8*24*time.Hour:
		return time.Hour
	}
	return 24 * time.Hour
}

// recordQuery 读取请求记录的范围、维度筛选、状态码、关键字、分页位置与页大小
func recordQuery(values url.Values) (requestdb.RecordQuery, error) {
	from, to, err := parseRange(values)
	if err != nil {
		return requestdb.RecordQuery{}, err
	}
	query := requestdb.RecordQuery{From: from, To: to, Filters: filters(values), Search: strings.TrimSpace(values.Get("q")), Limit: 50}
	if value := values.Get("status"); value != "" {
		if query.Status, err = strconv.Atoi(value); err != nil {
			return requestdb.RecordQuery{}, fmt.Errorf("status 必须是整数")
		}
	}
	if value := values.Get("limit"); value != "" {
		if query.Limit, err = strconv.Atoi(value); err != nil || query.Limit < 1 {
			return requestdb.RecordQuery{}, fmt.Errorf("limit 必须是正整数")
		}
	}
	if value := values.Get("cursor"); value != "" {
		cursor, err := requestdb.DecodeCursor(value)
		if err != nil {
			return requestdb.RecordQuery{}, err
		}
		query.Before = &cursor
	}
	return query, nil
}

// listRecords 按筛选返回一页请求记录
func (s *server) listRecords(w http.ResponseWriter, r *http.Request) {
	query, err := recordQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	items, next, err := s.ledger.Records(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "records_failed", err.Error())
		return
	}
	page := map[string]any{"items": items}
	if next != nil {
		page["next_cursor"] = next.Encode()
	}
	writeJSON(w, http.StatusOK, page)
}

// exportRecords 以 CSV 附件导出范围内全部符合筛选的请求记录
func (s *server) exportRecords(w http.ResponseWriter, r *http.Request) {
	query, err := recordQuery(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	name := fmt.Sprintf("antigravity2api-requests-%s-%s.csv", query.From.UTC().Format("20060102T150405Z"), query.To.UTC().Format("20060102T150405Z"))
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	if err := s.ledger.ExportRecords(r.Context(), query, w); err != nil {
		slog.ErrorContext(r.Context(), "请求记录导出失败 | 错误="+err.Error(), "source", "service")
	}
}

// getRecord 返回请求记录、公开协议正文与上游尝试正文
func (s *server) getRecord(w http.ResponseWriter, r *http.Request) {
	detail, err := s.ledger.Detail(r.Context(), r.PathValue("id"))
	if errors.Is(err, requestdb.ErrNotFound) {
		writeError(w, http.StatusNotFound, "record_not_found", err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "record_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

// listActive 返回进行中的公开请求
func (s *server) listActive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"requests": s.events.running()})
}

// cancelRequest 停止进行中的公开请求，结果经请求事件推送
func (s *server) cancelRequest(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.events.stop(id) {
		writeError(w, http.StatusNotFound, "request_not_found", "请求不存在或已结束: "+id)
		return
	}
	slog.WarnContext(r.Context(), "管理端停止请求 | 请求="+id, "source", "request")
	w.WriteHeader(http.StatusNoContent)
}

// listLogs 按筛选返回一页已入库的 WARN 以上日志
func (s *server) listLogs(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	from, to, err := parseRange(values)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	query := requestdb.LogQuery{
		From: from, To: to, Level: strings.ToUpper(values.Get("level")), Source: values.Get("source"),
		RequestID: values.Get("request_id"), Model: values.Get("model"), Search: strings.TrimSpace(values.Get("q")), Limit: 100,
	}
	if value := values.Get("limit"); value != "" {
		if query.Limit, err = strconv.Atoi(value); err != nil || query.Limit < 1 {
			writeError(w, http.StatusBadRequest, "invalid_request", "limit 必须是正整数")
			return
		}
	}
	if value := values.Get("before"); value != "" {
		if query.Before, err = strconv.ParseInt(value, 10, 64); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "before 必须是整数")
			return
		}
	}
	items, next, err := s.ledger.Logs(r.Context(), query)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "logs_failed", err.Error())
		return
	}
	page := map[string]any{"items": items}
	if next != 0 {
		page["next_before"] = next
	}
	writeJSON(w, http.StatusOK, page)
}

// clearLogs 清空内存与账本中的日志
func (s *server) clearLogs(w http.ResponseWriter, r *http.Request) {
	s.events.clearLogs()
	if err := s.ledger.ClearLogs(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "logs_clear_failed", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// filters 读取各维度的逗号分隔筛选值
func filters(values url.Values) map[string][]string {
	result := map[string][]string{}
	for _, dimension := range requestdb.Dimensions {
		result[dimension] = config.SplitList(values.Get(dimension))
	}
	return result
}

// parseRange 解析 RFC 3339 的 from 与 to
func parseRange(values url.Values) (time.Time, time.Time, error) {
	from, fromErr := time.Parse(time.RFC3339, values.Get("from"))
	to, toErr := time.Parse(time.RFC3339, values.Get("to"))
	if fromErr != nil || toErr != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("from 与 to 必须是 RFC 3339 时间")
	}
	return from, to, nil
}

// decodeJSON 解码 JSON 请求体
func decodeJSON(r *http.Request, target any) error {
	if err := json.NewDecoder(r.Body).Decode(target); err != nil {
		return fmt.Errorf("请求体不是有效的 JSON: %w", err)
	}
	return nil
}

// writeJSON 写出 JSON 响应
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

// writeError 写出 {"error":{"code","message"}} 错误响应
func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
