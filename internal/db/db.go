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
	"log/slog"
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

// Execer is the query surface every store talks through. *DB and *Tx both
// implement it, so a store can be re-bound to a transaction (its WithTx
// method) without touching any call site. Keep new query verbs here
// deliberately, like the methods above.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryRow(query string, args ...any) *sql.Row
	Insert(ctx context.Context, insert string, args ...any) (int64, error)
}

var (
	_ Execer = (*DB)(nil)
	_ Execer = (*Tx)(nil)
)

// Tx is a database transaction behind the same dialect-wrapped surface as
// DB: a multi-statement write commits (or rolls back) as one unit — and on
// sqlite that also means one commit instead of one fsync per statement.
type Tx struct {
	tx *sql.Tx
	d  Dialect
}

// BeginTx starts a transaction. sqlite runs on a single pooled connection,
// so other statements queue while a transaction is open; keep them short —
// a whole 2000-line document apply is well under a second.
func (db *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*Tx, error) {
	raw, err := db.DB.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: raw, d: db.d}, nil
}

// Commit and Rollback finish the transaction. After a successful Commit a
// Rollback returns sql.ErrTxDone rather than nil — harmless for the standard
// `defer tx.Rollback()` guard, which discards the error and is exactly why
// call sites write it that way.
func (t *Tx) Commit() error   { return t.tx.Commit() }
func (t *Tx) Rollback() error { return t.tx.Rollback() }

func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(ctx, t.d.Rebind(query), args...)
}

func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, t.d.Rebind(query), args...)
}

func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, t.d.Rebind(query), args...)
}

// QueryRow mirrors QueryRowContext without a context; transactions are
// never a boot-time construct, so it runs on a background context.
func (t *Tx) QueryRow(query string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(context.Background(), t.d.Rebind(query), args...)
}

// Insert mirrors DB.Insert for a transaction.
func (t *Tx) Insert(ctx context.Context, insert string, args ...any) (int64, error) {
	if t.d.IDReturning == "" {
		res, err := t.ExecContext(ctx, insert, args...)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	var id int64
	q := insert + " RETURNING " + t.d.IDReturning
	if err := t.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
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
		// Single connection: correct for ":memory:" (per-connection DBs
		// otherwise) and avoids SQLITE_BUSY contention for file databases
		// at this scale. Postgres pools properly, so the cap is sqlite-only.
		conn.SetMaxOpenConns(1)
		// foreign_keys: the core schema invariant. journal_mode=WAL is
		// persistent (recorded in the database file itself): a commit
		// then appends to the log instead of re-fsyncing the database,
		// which is the difference between milliseconds and seconds for
		// write-heavy operations on phone- or router-class flash, and it
		// lets reads run while a write transaction holds the write lock.
		// busy_timeout covers the rare overlap of two writers.
		for _, p := range []string{
			"PRAGMA foreign_keys = ON;",
			"PRAGMA busy_timeout = 5000;",
		} {
			if _, err := conn.Exec(p); err != nil {
				conn.Close()
				return nil, fmt.Errorf("pragma %q: %w", p, err)
			}
		}
		// WAL is the one preference that may legitimately fail — some
		// filesystems (vfat, certain network mounts) cannot host the
		// -shm file it needs. Fall back to the default journal rather
		// than refusing to start.
		if _, err := conn.Exec("PRAGMA journal_mode = WAL;"); err != nil {
			slog.Warn("WAL mode unavailable here; using the default rollback journal (writes will be slower)", "err", err)
		}
	}
	if err := migrate(context.Background(), conn, d); err != nil {
		conn.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &DB{DB: conn, d: d}, nil
}
