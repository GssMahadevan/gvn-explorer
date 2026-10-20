// Default SQLite driver. Pure Go, but its generated code is per-arch and
// excludes 32-bit MIPS (linux/mips, linux/mipsle — the usual OpenWrt
// routers). Builds for those targets use -tags sqlite_purego, which
// swaps in driver_purego.go instead. Both variants pass the same API
// suite (scripts/test_matrix.py).
//go:build !sqlite_purego

package db

import _ "modernc.org/sqlite"
