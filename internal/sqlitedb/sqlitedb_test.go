package sqlitedb

import (
	"database/sql"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestOpenAppliesSchema 验证已有库打开后与建表语句一致：保留已有记录，删除未定义的表与索引，库为增量收缩且空闲页交还文件系统
func TestOpenAppliesSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE items (id TEXT PRIMARY KEY, name TEXT NOT NULL);
INSERT INTO items VALUES ('a', 'first'), ('b', 'second');
CREATE INDEX items_name_legacy ON items(name);
CREATE TABLE legacy (data BLOB);
INSERT INTO legacy SELECT zeroblob(4096) FROM (WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 256) SELECT i FROM n);`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	schema := `CREATE TABLE IF NOT EXISTS items (id TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS items_name ON items(name);`
	db, err := Open(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	objects, err := schemaObjects(db)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(objects))
	for name, kind := range objects {
		names = append(names, kind+":"+name)
	}
	slices.Sort(names)
	if strings.Join(names, ",") != "index:items_name,table:items" {
		t.Fatalf("表结构对象 = %v", names)
	}
	var items string
	if err := db.QueryRow(`SELECT group_concat(id || '=' || name, ',') FROM (SELECT id, name FROM items ORDER BY id)`).Scan(&items); err != nil || items != "a=first,b=second" {
		t.Fatalf("已有记录 = %q err=%v", items, err)
	}
	var mode, free, pages int
	if err := db.QueryRow(`PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != incrementalVacuum {
		t.Fatalf("auto_vacuum = %d err=%v", mode, err)
	}
	if err := db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil || free != 0 {
		t.Fatalf("freelist_count = %d err=%v", free, err)
	}
	if err := db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil || pages > 16 {
		t.Fatalf("page_count = %d err=%v", pages, err)
	}
}
