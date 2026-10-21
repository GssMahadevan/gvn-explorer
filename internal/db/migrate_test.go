package db

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRebindDollar(t *testing.T) {
	cases := []struct{ in, want string }{
		{"SELECT 1", "SELECT 1"},
		{"WHERE a=? AND b=?", "WHERE a=$1 AND b=$2"},
		{`LIKE ? ESCAPE '\'`, `LIKE $1 ESCAPE '\'`}, // ? inside literal must not count
		{`LIKE '\%' AND x=?`, `LIKE '\%' AND x=$1`},
		{`"col?name" = ?`, `"col?name" = $1`},
		{`'', ?, ''''`, `'', $1, ''''`},
	}
	for _, c := range cases {
		if got := rebindDollar(c.in); got != c.want {
			t.Errorf("rebindDollar(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func columns(t *testing.T, conn *DB, table string) map[string]bool {
	t.Helper()
	rows, err := conn.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var (
			cid     int
			name    string
			ctype   string
			notnull int
			dflt    any
			pk      int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatal(err)
		}
		out[name] = true
	}
	return out
}

func appliedVersions(t *testing.T, conn *DB) []int {
	t.Helper()
	rows, err := conn.Query("SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	return out
}

// TestMigrationsFresh: a new database gets every migration, including the
// 0002 entity-meta columns.
func TestMigrationsFresh(t *testing.T) {
	conn, err := Open("sqlite", filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	got := appliedVersions(t, conn)
	if len(got) < 2 {
		t.Fatalf("applied %v, want >=2 versions", got)
	}
	for _, table := range []string{"users", "groups", "tenants"} {
		cols := columns(t, conn, table)
		for _, col := range []string{"description", "active"} {
			if !cols[col] {
				t.Errorf("%s.%s missing after migrations", table, col)
			}
		}
	}
}

// TestMigrationsBaseline: a database built before the migration mechanism
// (tables present, no schema_migrations history) is baselined at 0001 and
// then upgraded by 0002 — the columns come back after the simulated drop.
func TestMigrationsBaseline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// roll the entity-meta migration back by hand: wipe history and drop
	// exactly what 0002 added, leaving the 0001 schema as a pre-mechanism
	// database would have it.
	for _, stmt := range []string{
		"DELETE FROM schema_migrations",
		"ALTER TABLE users DROP COLUMN description",
		"ALTER TABLE groups DROP COLUMN description",
		"ALTER TABLE tenants DROP COLUMN description",
		"ALTER TABLE tenants DROP COLUMN active",
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	conn.Close()

	conn2, err := Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	got := appliedVersions(t, conn2)
	if len(got) < 2 {
		t.Fatalf("after baseline applied %v, want >=2 versions", got)
	}
	for _, table := range []string{"users", "groups", "tenants"} {
		if !columns(t, conn2, table)["description"] {
			t.Errorf("%s.description missing after baseline upgrade", table)
		}
	}
	if !columns(t, conn2, "tenants")["active"] {
		t.Error("tenants.active missing after baseline upgrade")
	}
}

// TestMigrationsTsMillis: rows written before 0006 hold seconds, and the
// upgrade multiplies them to milliseconds exactly once. The guard also
// makes re-running the statement harmless (an idempotency the long-lived
// DBs on phones benefit from).
func TestMigrationsTsMillis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	conn, err := Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"DELETE FROM schema_migrations WHERE version >= 6",
		"INSERT INTO events (ts, tenant_id, action) VALUES (1755000000, 1, 'login')",
		"INSERT INTO file_drafts (path, content, updated_by, updated_at) VALUES ('/a', 'x', 'sys/admin', 1755000000)",
	} {
		if _, err := conn.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	conn.Close()

	conn2, err := Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn2.Close()
	var got int64
	for q, want := range map[string]int64{
		"SELECT ts FROM events WHERE action = 'login'":         1755000000000,
		"SELECT updated_at FROM file_drafts WHERE path = '/a'": 1755000000000,
	} {
		if err := conn2.QueryRow(q).Scan(&got); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got != want {
			t.Errorf("%s = %d, want %d (seconds must scale exactly once)", q, got, want)
		}
	}
}

// TestOpenUnknownDriver: engines without a dialect are rejected loudly.
func TestOpenUnknownDriver(t *testing.T) {
	if _, err := Open("mysql", "whatever"); err == nil {
		t.Fatal("expected error for unsupported driver")
	}
}

// TestInsertID: the DB.Insert helper returns the new row id on sqlite.
func TestInsertID(t *testing.T) {
	conn, err := Open("sqlite", filepath.Join(t.TempDir(), "i.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	id1, err := conn.Insert(context.Background(),
		"INSERT INTO tenants (name, created_at) VALUES (?,?)", "t1", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	id2, err := conn.Insert(context.Background(),
		"INSERT INTO tenants (name, created_at) VALUES (?,?)", "t2", "2026-01-01T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if id2 != id1+1 || id1 == 0 {
		t.Fatalf("ids %d,%d not sequential from 1", id1, id2)
	}
}
