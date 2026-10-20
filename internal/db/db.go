// Package db opens the application database and owns the schema lifecycle:
// versioned, dialect-scoped migration files applied under a
// schema_migrations bookkeeping table.
//
// The permission model itself (users, groups, memberships, per-path ACLs,
// sessions) is platform-independent; the files they guard live on the real
// filesystem under the server's root. SQLite is the built-in engine today.
// Everything above this package talks to DB (below), whose Exec/Query/Insert
// helpers keep SQL portable so moving to Postgres means: import a driver,
// add its migrations/postgres/*.sql, adjust the noted app-level conventions
// (case-insensitive matching) — not rewriting call sites.
package db

import (
	"context"
	"database/sql"
	"fmt"
)

// The SQLite driver itself is imported from a build-tagged file
// (driver_modernc.go / driver_purego.go): modernc is the default, and
// -tags sqlite_purego swaps in an arch-independent pure-Go port so the
// server cross-compiles for 32-bit MIPS (the common OpenWrt targets,
// which modernc cannot generate code for).

// DB wraps *sql.DB behind the engine's Dialect: the same `?`-placeholder SQL
// is rewritten per engine where needed, and Insert retrieves new ids the way
// the engine hands them back. Call sites (auth/acl/audit stores) use DB
// instead of *sql.DB; unlisted methods would silently bypass the dialect, so
// keep new query verbs here deliberately.
type DB struct {
	*sql.DB
	d Dialect
}

// Dialect returns the active engine descriptor.
func (db *DB) Dialect() Dialect { return db.d }

// ExecContext / QueryContext / QueryRowContext rebind the query for the
// engine before delegating. (Signatures mirror *sql.DB so stores that hold a
// *DB use them transparently.)
func (db *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return db.DB.ExecContext(ctx, db.d.Rebind(query), args...)
}

func (db *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return db.DB.QueryContext(ctx, db.d.Rebind(query), args...)
}

func (db *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return db.DB.QueryRowContext(ctx, db.d.Rebind(query), args...)
}

// QueryRow mirrors QueryRowContext without a context, for boot-time code.
func (db *DB) QueryRow(query string, args ...any) *sql.Row {
	return db.DB.QueryRow(db.d.Rebind(query), args...)
}

// Exec mirrors ExecContext without a context, for boot-time code.
func (db *DB) Exec(query string, args ...any) (sql.Result, error) {
	return db.DB.Exec(db.d.Rebind(query), args...)
}

// Insert runs an INSERT and returns the new row's id. SQLite reports it via
// LastInsertId; Postgres-style engines get " RETURNING id" appended and the
// id scanned back. Call sites therefore write plain INSERTs with `?` marks.
func (db *DB) Insert(ctx context.Context, insert string, args ...any) (int64, error) {
	if db.d.IDReturning == "" {
		res, err := db.ExecContext(ctx, insert, args...)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	var id int64
	q := insert + " RETURNING " + db.d.IDReturning
	if err := db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

// Open opens the database for driver ("sqlite" is built in; "postgres"
// needs a driver import) at dsn and applies pending migrations.
func Open(driver, dsn string) (*DB, error) {
	var d Dialect
	switch driver {
	case "sqlite":
		d = sqliteDialect
	case "postgres":
		d = postgresDialect
	default:
		return nil, fmt.Errorf("unsupported driver %q (sqlite is built in; other engines need a driver import and a Dialect entry)", driver)
	}

	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	if driver == "sqlite" {
		if _, err := conn.Exec("PRAGMA foreign_keys = ON;"); err != nil {
			conn.Close()
			return nil, err
		}
		// Single connection: correct for ":memory:" (per-connection DBs
		// otherwise) and avoids SQLITE_BUSY contention for file databases
		// at this scale. Postgres pools properly, so the cap is sqlite-only.
		conn.SetMaxOpenConns(1)
	}
	if err := migrate(context.Background(), conn, d); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{DB: conn, d: d}, nil
}
