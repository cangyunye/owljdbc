package owljdbc

import (
	"context"
	"database/sql"
	"testing"
)

func TestDriverOpenQuery(t *testing.T) {
	proc := newTestProc(t)
	cfg := Config{Family: "mysql"}
	dsn := EncodeDSN(cfg)
	// 测试模式：用 fake agent 进程替换 DefaultManager 的 profile，避免真 spawn java。
	DefaultManager.mu.Lock()
	DefaultManager.procs[profileKey(cfg)] = &procEntry{proc: proc, refs: 1}
	DefaultManager.mu.Unlock()
	t.Cleanup(func() {
		DefaultManager.mu.Lock()
		delete(DefaultManager.procs, profileKey(cfg))
		DefaultManager.mu.Unlock()
	})

	db, err := sql.Open("owljdbc", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var s string
	if err := db.QueryRowContext(context.Background(), "SELECT 1").Scan(&s); err != nil {
		t.Fatal(err)
	}
	if s != "hello" {
		t.Fatalf("got %q", s)
	}
}

func TestDriverExec(t *testing.T) {
	proc := newTestProc(t)
	cfg := Config{Family: "mysql"}
	DefaultManager.mu.Lock()
	DefaultManager.procs[profileKey(cfg)] = &procEntry{proc: proc, refs: 1}
	DefaultManager.mu.Unlock()
	t.Cleanup(func() {
		DefaultManager.mu.Lock()
		delete(DefaultManager.procs, profileKey(cfg))
		DefaultManager.mu.Unlock()
	})

	db, err := sql.Open("owljdbc", EncodeDSN(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	res, err := db.ExecContext(context.Background(), "INSERT INTO t VALUES(?,?)", int64(1), "x")
	if err != nil {
		t.Fatal(err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("affected = %d", n)
	}
}
