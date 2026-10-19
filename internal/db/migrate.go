package db

// Versioned schema migrations. Each engine keeps its own directory of SQL
// files (migrations/<dialect>/NNNN_slug.sql); they are applied in filename
// order, once each, tracked in a shared schema_migrations bookkeeping table.
//
// Databases created before this mechanism (by the old inline schema string)
// are baselined: when the tenants table already exists and no migration is
// recorded, the first migration is marked applied without executing it —
// its content is exactly what those databases already have.

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations
var migrationFS embed.FS

// migration is one discovered, not-yet-applied SQL file.
type migration struct {
	version int
	name    string
	data    string
}

func migrate(ctx context.Context, conn *sql.DB, d Dialect) error {
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version    INTEGER PRIMARY KEY,
			name       TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return err
	}

	dir := "migrations/" + d.Name
	files, err := fs.ReadDir(migrationFS, dir)
	if err != nil {
		return fmt.Errorf("no migrations for engine %q: %w", d.Name, err)
	}
	var all []migration
	for _, f := range files { // fs.ReadDir returns names sorted
		name := f.Name()
		if f.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		digits, _, ok := strings.Cut(name, "_")
		if !ok {
			return fmt.Errorf("migration %q must be named NNNN_slug.sql", name)
		}
		version, err := strconv.Atoi(digits)
		if err != nil {
			return fmt.Errorf("migration %q must start with a number: %w", name, err)
		}
		data, err := migrationFS.ReadFile(dir + "/" + name)
		if err != nil {
			return err
		}
		all = append(all, migration{version: version, name: name, data: string(data)})
	}

	applied := map[int]bool{}
	rows, err := conn.QueryContext(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return err
	}
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			rows.Close()
			return err
		}
		applied[v] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	// Pre-mechanism databases: baseline the first migration, then upgrade
	// forward normally.
	if len(applied) == 0 && len(all) > 0 && tableExists(ctx, conn, "tenants") {
		if _, err := conn.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)",
			all[0].version, "BASELINE "+all[0].name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		applied[all[0].version] = true
	}

	for _, m := range all {
		if applied[m.version] {
			continue
		}
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, m.data); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, applied_at) VALUES (?,?,?)",
			m.version, m.name, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func tableExists(ctx context.Context, conn *sql.DB, table string) bool {
	var n int
	err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&n)
	return err == nil && n > 0
}
