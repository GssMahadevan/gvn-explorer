//go:build sqlite_purego

package db

import (
	"database/sql"

	nc "github.com/ncruces/go-sqlite3/driver"
)

// ncruces/go-sqlite3 is SQLite compiled to WebAssembly and translated to
// plain Go functions — no assembly, no cgo, so it builds for any GOARCH
// Go itself supports, including mips/mipsle softfloat. It registers
// itself as "sqlite3"; the rest of this package speaks the modernc name
// "sqlite" (see the switch in Open), so alias it here. Nothing else
// changes: the DSN is a plain file path and we only use database/sql
// plus plain PRAGMA execs.
func init() { sql.Register("sqlite", &nc.SQLite{}) }
