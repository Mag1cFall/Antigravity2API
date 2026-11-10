// Package sqlitedb 打开服务的本地 SQLite 数据库，按建表语句同步表结构并把空闲页交还文件系统
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// pragmas 是每个新连接执行的设置，驱动先执行 busy_timeout，其余按名称顺序执行，增量收缩因此先于 WAL 生效
var pragmas = url.Values{"_pragma": {"auto_vacuum(INCREMENTAL)", "busy_timeout(10000)", "journal_mode(WAL)", "journal_size_limit(524288)", "synchronous(NORMAL)"}}

// incrementalVacuum 是 PRAGMA auto_vacuum 表示增量收缩的值
const incrementalVacuum = 2

// Open 打开或创建数据库，执行 schema 并删除 schema 没有定义的对象，非增量收缩的已有库整理为增量收缩，最后交还空闲页
func Open(path string, schema string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?"+pragmas.Encode())
	if err != nil {
		return nil, err
	}
	if err := applySchema(db, schema); err != nil {
		return nil, errors.Join(fmt.Errorf("同步表结构: %w", err), db.Close())
	}
	var mode int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil {
		return nil, errors.Join(fmt.Errorf("读取收缩模式: %w", err), db.Close())
	}
	if mode != incrementalVacuum {
		if _, err := db.Exec(`VACUUM`); err != nil {
			return nil, errors.Join(fmt.Errorf("整理数据库: %w", err), db.Close())
		}
	}
	if err := Reclaim(context.Background(), db); err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return db, nil
}

// applySchema 执行 schema，再删除 schema 没有定义的表、索引、视图与触发器
func applySchema(db *sql.DB, schema string) error {
	defined, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer defined.Close()
	defined.SetMaxOpenConns(1)
	if _, err := defined.Exec(schema); err != nil {
		return err
	}
	want, err := schemaObjects(defined)
	if err != nil {
		return err
	}
	have, err := schemaObjects(db)
	if err != nil {
		return err
	}
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	for name, kind := range have {
		if _, ok := want[name]; !ok {
			if _, err := db.Exec(`DROP ` + strings.ToUpper(kind) + ` IF EXISTS ` + quoteIdentifier(name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// schemaObjects 返回库中除 SQLite 内部对象外的表、索引、视图与触发器的名称与类型
func schemaObjects(db *sql.DB) (map[string]string, error) {
	rows, err := db.Query(`SELECT name, type FROM sqlite_master WHERE type IN ('table', 'index', 'view', 'trigger') AND name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	objects := make(map[string]string)
	for rows.Next() {
		var name, kind string
		if err := rows.Scan(&name, &kind); err != nil {
			return nil, err
		}
		objects[name] = kind
	}
	return objects, rows.Err()
}

// quoteIdentifier 返回双引号括起的 SQL 标识符
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// Reclaim 把空闲页交还文件系统并截短 WAL 文件，incremental_vacuum 每交还一页返回一行，读完全部行才交还全部空闲页
func Reclaim(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `PRAGMA incremental_vacuum`)
	if err != nil {
		return fmt.Errorf("交还空闲页: %w", err)
	}
	for rows.Next() {
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("交还空闲页: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		return fmt.Errorf("截短 WAL: %w", err)
	}
	return nil
}
