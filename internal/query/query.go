// Package query carries the list modifiers shared by every list endpoint:
// paging (limit/offset), column search (q/col) and sorting (sort/dir).
// Handlers validate the values against a per-endpoint whitelist; stores
// translate them into SQL.
package query

import "strings"

// List holds one request's validated list modifiers.
type List struct {
	Limit  int    // rows per page; 0 = every row (the historical default)
	Offset int    // rows to skip
	Q      string // case-insensitive substring filter; "" = no filter
	Col    string // restrict Q to one column; "" = all searchable columns
	Sort   string // sort key, already validated against the endpoint whitelist
	Dir    string // "asc" or "desc"
}

// Like wraps Q as a SQL LIKE pattern, escaping LIKE metacharacters.
// Callers must add ESCAPE '\' to the LIKE clause.
func (l List) Like() string {
	q := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(l.Q)
	return "%" + q + "%"
}

// Order returns the direction suffix for an ORDER BY clause.
func (l List) Order() string {
	if l.Dir == "desc" {
		return " desc"
	}
	return " asc"
}
