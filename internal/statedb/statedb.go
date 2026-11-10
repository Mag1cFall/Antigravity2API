// Package statedb 在本地 SQLite 中保存公开 API 的续接节点
package statedb

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Mag1cFall/Antigravity2API/internal/api"
	"github.com/Mag1cFall/Antigravity2API/internal/sqlitedb"
)

const schema = `
CREATE TABLE IF NOT EXISTS states (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, scope TEXT NOT NULL, id TEXT NOT NULL, data BLOB NOT NULL, UNIQUE (scope, id));`

// DB 是续接状态库
type DB struct {
	db *sql.DB
}

// Open 打开或创建续接状态库
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sqlitedb.Open(path, schema)
	if err != nil {
		return nil, fmt.Errorf("初始化续接状态库: %w", err)
	}
	return &DB{db: db}, nil
}

// Close 关闭续接状态库
func (d *DB) Close() error {
	return d.db.Close()
}

// Scope 返回 scope 下的续接节点存储，Responses 与 Interactions 各用一个 scope
func (d *DB) Scope(scope string) api.StateStore {
	return &store{db: d.db, scope: scope}
}

// store 是一个 scope 下的续接节点存储
type store struct {
	db    *sql.DB
	scope string
}

// Load 按保存顺序读出全部节点，读取失败时记录日志并返回已读到的节点
func (s *store) Load() []api.SavedState {
	states, err := s.load()
	if err != nil {
		slog.Warn("读取续接状态失败 | 错误="+err.Error(), "source", "service")
	}
	return states
}

func (s *store) load() ([]api.SavedState, error) {
	rows, err := s.db.Query(`SELECT id, data FROM states WHERE scope = ? ORDER BY seq`, s.scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var states []api.SavedState
	for rows.Next() {
		var state api.SavedState
		if err := rows.Scan(&state.ID, &state.Data); err != nil {
			return states, err
		}
		states = append(states, state)
	}
	return states, rows.Err()
}

// Save 在一个事务中写入节点并删除已移除的节点，已有节点保留原保存顺序，失败时记录日志
func (s *store) Save(saved []api.SavedState, removed []string) {
	if err := s.save(saved, removed); err != nil {
		slog.Warn("保存续接状态失败 | 错误="+err.Error(), "source", "service")
	}
}

func (s *store) save(saved []api.SavedState, removed []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, state := range saved {
		if _, err := tx.Exec(`INSERT INTO states (scope, id, data) VALUES (?, ?, ?) ON CONFLICT (scope, id) DO UPDATE SET data = excluded.data`, s.scope, state.ID, state.Data); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	for _, id := range removed {
		if _, err := tx.Exec(`DELETE FROM states WHERE scope = ? AND id = ?`, s.scope, id); err != nil {
			return errors.Join(err, tx.Rollback())
		}
	}
	return tx.Commit()
}
