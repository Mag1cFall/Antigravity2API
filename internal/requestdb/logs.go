package requestdb

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// logData 是 logs 表 data 列中的请求记录与其余属性
type logData struct {
	Request *Row           `json:"request,omitempty"`
	Attrs   map[string]any `json:"attrs,omitempty"`
}

// LogQuery 选择日志的时间范围、级别、来源、请求 ID、模型、关键字与分页位置
type LogQuery struct {
	From time.Time
	To   time.Time
	// Level 为 ERROR 时只返回 ERROR，其余取值返回全部已保存的日志
	Level     string
	Source    string
	RequestID string
	Model     string
	// Search 匹配消息与属性文本
	Search string
	// Before 为 0 时从最新一条开始，否则返回序号更小的日志
	Before int64
	Limit  int
}

// Logs 按写入顺序倒序返回一页日志，next 为 0 表示没有更早的日志
func (store *Store) Logs(ctx context.Context, query LogQuery) ([]Log, int64, error) {
	clause := ` WHERE time >= ? AND time < ?`
	args := []any{query.From.UnixMilli(), query.To.UnixMilli()}
	for column, value := range map[string]string{"source": query.Source, "request_id": query.RequestID, "model": query.Model} {
		if value != "" {
			clause += ` AND ` + column + ` = ?`
			args = append(args, value)
		}
	}
	if query.Level == "ERROR" {
		clause += ` AND level = 'ERROR'`
	}
	if query.Search != "" {
		pattern := "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query.Search) + "%"
		clause += ` AND (message LIKE ? ESCAPE '\' OR data LIKE ? ESCAPE '\')`
		args = append(args, pattern, pattern)
	}
	if query.Before > 0 {
		clause += ` AND seq < ?`
		args = append(args, query.Before)
	}
	rows, err := store.db.QueryContext(ctx, `SELECT seq, time, level, source, event, message, request_id, model, data FROM logs`+
		clause+` ORDER BY seq DESC LIMIT ?`, append(args, query.Limit+1)...)
	if err != nil {
		return nil, 0, err
	}
	items := []Log{}
	for rows.Next() {
		var entry Log
		var at int64
		var data string
		if err := rows.Scan(&entry.Seq, &at, &entry.Level, &entry.Source, &entry.Event, &entry.Message, &entry.RequestID, &entry.Model, &data); err != nil {
			return nil, 0, errors.Join(err, rows.Close())
		}
		var extra logData
		if err := json.Unmarshal([]byte(data), &extra); err != nil {
			return nil, 0, errors.Join(err, rows.Close())
		}
		entry.Time, entry.Request, entry.Attrs = time.UnixMilli(at).UTC(), extra.Request, extra.Attrs
		items = append(items, entry)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, 0, err
	}
	if len(items) <= query.Limit {
		return items, 0, nil
	}
	items = items[:query.Limit]
	return items, items[len(items)-1].Seq, nil
}

// ClearLogs 删除全部已保存的日志
func (store *Store) ClearLogs(ctx context.Context) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM logs`)
	return err
}
