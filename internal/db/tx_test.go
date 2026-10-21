package db

// The transaction surface (BeginTx / Tx / Execer) and the WAL pragma that
// makes write-heavy operations cheap on slow flash. Import apply depends on
// both: one commit per document, and all-or-nothing on mid-apply failure.

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func openFileDB(t *testing.T) *DB {
	t.Helper()
	conn, err := Open("sqlite", filepath.Join(t.TempDir(), "tx.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// insertTenant matches the schema's NOT NULL created_at with a literal —
// the point of these tests is the transaction, not the timestamps.
const insertTenant = "INSERT INTO tenants (name, created_at) VALUES (?, datetime('now'))"

// journalMode is persistent in the file itself, so a freshly opened file db
// must report WAL — that is the whole point of setting it at Open.
func TestWALEnabledOnFileDB(t *testing.T) {
	conn := openFileDB(t)
	var mode string
	if err := conn.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

func TestTxRollbackDiscardsCommitPersists(t *testing.T) {
	conn := openFileDB(t)
	ctx := context.Background()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, insertTenant, "doomed"); err != nil {
		t.Fatal(err)
	}
	// A read inside the same tx sees the uncommitted row.
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants WHERE name=?", "doomed").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("in-tx read should see its own write, got %d", n)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	// defer-Rollback after a real Commit must be a harmless no-op.
	tx2, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx2.ExecContext(ctx, insertTenant, "kept"); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); !errors.Is(err, sql.ErrTxDone) {
		t.Fatalf("rollback after commit should report ErrTxDone (the deferred guard discards it), got %v", err)
	}

	var doomed, kept int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants WHERE name='doomed'").Scan(&doomed); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants WHERE name='kept'").Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if doomed != 0 {
		t.Errorf("rolled-back row survived: %d", doomed)
	}
	if kept != 1 {
		t.Errorf("committed row missing: %d", kept)
	}
}

// Rebinding a query surface to a transaction must be usable through the same
// Execer methods callers use outside one — the mechanism WithTx relies on.
func TestExecerSurfaceUsableThroughTx(t *testing.T) {
	conn := openFileDB(t)
	ctx := context.Background()

	var surface Execer = conn
	id, err := surface.Insert(ctx, insertTenant, "committed-via-interface")
	if err != nil || id == 0 {
		t.Fatalf("interface insert outside tx: id=%d err=%v", id, err)
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	surface = tx
	if _, err := surface.Insert(ctx, insertTenant, "rolled-back-via-interface"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var seen, gone int
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants WHERE name='committed-via-interface'").Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM tenants WHERE name='rolled-back-via-interface'").Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if seen != 1 || gone != 0 {
		t.Fatalf("interface inserts across tx boundary: seen=%d gone=%d, want 1/0", seen, gone)
	}
}
