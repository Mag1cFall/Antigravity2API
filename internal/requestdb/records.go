package requestdb

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ErrNotFound 表示请求记录不存在
var ErrNotFound = errors.New("请求记录不存在")

// rowColumns 是 Row 对应的列
const rowColumns = `id, time, client_request_id, response_id, protocol, path, api_key, client_ip, user_agent, stream,
 model, upstream_model, model_version, request_type, prompt_tier, cascade_id, step_index, account, quota_group,
 status, state, error_phase, upstream_status, error_code, duration_ms, first_event_ms, queue_ms,
 input_tokens, cached_tokens, reasoning_tokens, reply_tokens, tool_calls, cost_usd, error, attempts`

// Cursor 是请求记录的分页位置，下一页从该记录之后的更早记录开始
type Cursor struct {
	Time time.Time
	ID   string
}

// Encode 把分页位置编码为 URL 安全的文本
func (cursor Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(cursor.Time.UnixMilli(), 10) + "|" + cursor.ID))
}

// DecodeCursor 解析 Encode 生成的分页位置
func DecodeCursor(value string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	at, id, found := strings.Cut(string(raw), "|")
	milliseconds, parseErr := strconv.ParseInt(at, 10, 64)
	if err != nil || !found || parseErr != nil || id == "" {
		return Cursor{}, fmt.Errorf("cursor 无效")
	}
	return Cursor{Time: time.UnixMilli(milliseconds).UTC(), ID: id}, nil
}

// RecordQuery 选择请求记录的完成时间范围、维度筛选、状态码、关键字与分页位置
type RecordQuery struct {
	From time.Time
	To   time.Time
	// Filters 按维度限定取值，同一维度的多个取值为或关系
	Filters map[string][]string
	// Status 为 0 时不限状态码
	Status int
	// Search 匹配请求 ID、客户端请求 ID、响应 ID、cascade_id 与错误文本
	Search string
	Before *Cursor
	Limit  int
}

// Detail 是一条请求记录、公开协议正文与按尝试顺序排列的上游正文
type Detail struct {
	Record   Row    `json:"record"`
	Body     *Body  `json:"body"`
	Upstream []Body `json:"upstream"`
}

// recordFilter 返回范围、筛选、状态码与关键字对应的 SQL 条件与参数
func recordFilter(query RecordQuery) (string, []any) {
	clause, args := filterSQL(query.Filters)
	clause = ` WHERE time >= ? AND time < ?` + clause
	args = append([]any{query.From.UnixMilli(), query.To.UnixMilli()}, args...)
	if query.Status != 0 {
		clause += ` AND status = ?`
		args = append(args, query.Status)
	}
	if query.Search != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query.Search) + "%"
		clause += ` AND (id LIKE ? ESCAPE '\' OR client_request_id LIKE ? ESCAPE '\' OR response_id LIKE ? ESCAPE '\'` +
			` OR cascade_id LIKE ? ESCAPE '\' OR error LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern, pattern, pattern, pattern)
	}
	return clause, args
}

// Records 按完成时间倒序返回一页请求记录，next 为 nil 表示没有更早的记录
func (store *Store) Records(ctx context.Context, query RecordQuery) ([]Row, *Cursor, error) {
	clause, args := recordFilter(query)
	if before := query.Before; before != nil {
		clause += ` AND (time < ? OR time = ? AND id < ?)`
		args = append(args, before.Time.UnixMilli(), before.Time.UnixMilli(), before.ID)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT `+rowColumns+` FROM requests`+clause+` ORDER BY time DESC, id DESC LIMIT ?`,
		append(args, query.Limit+1)...)
	if err != nil {
		return nil, nil, err
	}
	items := []Row{}
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return nil, nil, errors.Join(err, rows.Close())
		}
		items = append(items, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, err
	}
	if len(items) <= query.Limit {
		return items, nil, nil
	}
	items = items[:query.Limit]
	last := items[len(items)-1]
	return items, &Cursor{Time: last.Time, ID: last.ID}, nil
}

// csvHeader 是导出文件的列名
var csvHeader = strings.Fields(strings.ReplaceAll(rowColumns, ",", " "))

// csvText 为可能被电子表格当作公式的文本加单引号前缀
func csvText(value string) string {
	if value != "" && strings.ContainsRune("=+-@\t\r", rune(value[0])) {
		return "'" + value
	}
	return value
}

// ExportRecords 以带 BOM 的 UTF-8 CSV 按完成时间倒序写出范围内全部符合条件的请求记录
func (store *Store) ExportRecords(ctx context.Context, query RecordQuery, output io.Writer) error {
	clause, args := recordFilter(query)
	rows, err := store.db.QueryContext(ctx, `SELECT `+rowColumns+` FROM requests`+clause+` ORDER BY time DESC, id DESC`, args...)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(output, "\xef\xbb\xbf"); err != nil {
		return errors.Join(err, rows.Close())
	}
	writer := csv.NewWriter(output)
	if err := writer.Write(csvHeader); err != nil {
		return errors.Join(err, rows.Close())
	}
	integer := func(value int64) string { return strconv.FormatInt(value, 10) }
	costText := func(value *float64) string {
		if value == nil {
			return ""
		}
		return strconv.FormatFloat(*value, 'f', -1, 64)
	}
	for rows.Next() {
		row, err := scanRow(rows)
		if err != nil {
			return errors.Join(err, rows.Close())
		}
		attempts, err := json.Marshal(row.Attempts)
		if err != nil {
			return errors.Join(err, rows.Close())
		}
		if err := writer.Write([]string{
			row.ID, row.Time.Format(time.RFC3339Nano), csvText(row.ClientRequestID), csvText(row.ResponseID), row.Protocol,
			csvText(row.Path), csvText(row.APIKey), row.ClientIP, csvText(row.UserAgent), strconv.FormatBool(row.Stream),
			csvText(row.Model), csvText(row.UpstreamModel), csvText(row.ModelVersion), csvText(row.RequestType), row.PromptTier,
			csvText(row.CascadeID), strconv.Itoa(row.StepIndex), csvText(row.Account), row.QuotaGroup, strconv.Itoa(row.Status),
			row.State, row.ErrorPhase, strconv.Itoa(row.UpstreamStatus), csvText(row.ErrorCode), integer(row.DurationMS),
			integer(row.FirstEventMS), integer(row.QueueMS), integer(row.InputTokens), integer(row.CachedTokens),
			integer(row.ReasoningTokens), integer(row.ReplyTokens), strconv.Itoa(row.ToolCalls), costText(row.CostUSD), csvText(row.Error), string(attempts),
		}); err != nil {
			return errors.Join(err, rows.Close())
		}
	}
	writer.Flush()
	return errors.Join(rows.Err(), rows.Close(), writer.Error())
}

// Detail 返回一条请求记录、公开协议正文与上游尝试正文，未保存的公开正文为 nil
func (store *Store) Detail(ctx context.Context, id string) (Detail, error) {
	rows, err := store.db.QueryContext(ctx, `SELECT `+rowColumns+` FROM requests WHERE id = ?`, id)
	if err != nil {
		return Detail{}, err
	}
	var detail Detail
	found := rows.Next()
	if found {
		detail.Record, err = scanRow(rows)
	}
	if err := errors.Join(err, rows.Err(), rows.Close()); err != nil {
		return Detail{}, err
	}
	if !found {
		return Detail{}, ErrNotFound
	}
	body := &Body{}
	err = store.db.QueryRowContext(ctx, `SELECT request, request_size, response, response_size FROM bodies WHERE id = ?`, id).
		Scan(&body.Request, &body.RequestSize, &body.Response, &body.ResponseSize)
	switch {
	case err == nil:
		detail.Body = body
	case !errors.Is(err, sql.ErrNoRows):
		return Detail{}, err
	}
	upstream, err := store.db.QueryContext(ctx, `SELECT request, request_size, response, response_size FROM upstream WHERE id = ? ORDER BY attempt`, id)
	if err != nil {
		return Detail{}, err
	}
	detail.Upstream = []Body{}
	for upstream.Next() {
		var body Body
		if err := upstream.Scan(&body.Request, &body.RequestSize, &body.Response, &body.ResponseSize); err != nil {
			return Detail{}, errors.Join(err, upstream.Close())
		}
		detail.Upstream = append(detail.Upstream, body)
	}
	return detail, errors.Join(upstream.Err(), upstream.Close())
}

// scanRow 读取 rowColumns 对应的一行
func scanRow(rows *sql.Rows) (Row, error) {
	var row Row
	var at int64
	var attempts string
	var costUSD sql.NullFloat64
	err := rows.Scan(&row.ID, &at, &row.ClientRequestID, &row.ResponseID, &row.Protocol, &row.Path, &row.APIKey, &row.ClientIP,
		&row.UserAgent, &row.Stream, &row.Model, &row.UpstreamModel, &row.ModelVersion, &row.RequestType, &row.PromptTier,
		&row.CascadeID, &row.StepIndex, &row.Account, &row.QuotaGroup, &row.Status, &row.State, &row.ErrorPhase,
		&row.UpstreamStatus, &row.ErrorCode, &row.DurationMS, &row.FirstEventMS, &row.QueueMS, &row.InputTokens,
		&row.CachedTokens, &row.ReasoningTokens, &row.ReplyTokens, &row.ToolCalls, &costUSD, &row.Error, &attempts)
	if err != nil {
		return Row{}, err
	}
	row.Time = time.UnixMilli(at).UTC()
	if costUSD.Valid {
		row.CostUSD = &costUSD.Float64
	}
	return row, json.Unmarshal([]byte(attempts), &row.Attempts)
}
