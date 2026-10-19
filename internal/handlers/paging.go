package handlers

// Paging, search and sort plumbing shared by every list endpoint. The GUI
// panels and curl scripts drive all lists with the same query parameters:
// limit, offset, q, col, sort, dir. X-Total-Count carries the number of
// rows that match the filters, so callers can compute the last page.

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/user/llm-crud1/internal/query"
)

// ListSpec declares one endpoint's searchable and sortable columns.
// Anything outside these whitelists is a 400, so typos surface loudly
// instead of silently changing what a query means.
type ListSpec struct {
	Sorts        map[string]bool // allowed sort keys
	Cols         []string        // allowed col= values
	DefSort      string          // sort key when sort is omitted
	DefDir       string          // direction when dir is omitted ("" = asc)
	ActiveFilter bool            // endpoint accepts active=true|false (entity lists)
}

// parseList reads limit/offset/q/col/sort/dir (and active, for specs that
// opt in) from the URL query. Omitting limit keeps the historical "every
// row" behaviour scripts rely on; stores add LIMIT/OFFSET only when Limit > 0.
func parseList(w http.ResponseWriter, r *http.Request, spec ListSpec) (query.List, bool) {
	var l query.List
	qs := r.URL.Query()
	if v := qs.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 1000 {
			writeError(w, http.StatusBadRequest, "bad_request", "limit must be an integer 1..1000")
			return l, false
		}
		l.Limit = n
	}
	if v := qs.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "bad_request", "offset must be a non-negative integer")
			return l, false
		}
		l.Offset = n
	}
	l.Q = strings.TrimSpace(qs.Get("q"))
	if len(l.Q) > 100 {
		writeError(w, http.StatusBadRequest, "bad_request", "q must be at most 100 characters")
		return l, false
	}
	if col := qs.Get("col"); col != "" {
		if !containsStr(spec.Cols, col) {
			writeError(w, http.StatusBadRequest, "bad_request", "col must be one of: "+strings.Join(spec.Cols, ", "))
			return l, false
		}
		l.Col = col
	}
	l.Sort = qs.Get("sort")
	if l.Sort == "" {
		l.Sort = spec.DefSort
	}
	if !spec.Sorts[l.Sort] {
		writeError(w, http.StatusBadRequest, "bad_request", "sort must be one of: "+sortedKeys(spec.Sorts))
		return l, false
	}
	l.Dir = strings.ToLower(qs.Get("dir"))
	if l.Dir == "" {
		l.Dir = spec.DefDir
		if l.Dir == "" {
			l.Dir = "asc"
		}
	}
	if l.Dir != "asc" && l.Dir != "desc" {
		writeError(w, http.StatusBadRequest, "bad_request", "dir must be asc or desc")
		return l, false
	}
	if v := strings.ToLower(qs.Get("active")); v != "" {
		if !spec.ActiveFilter {
			writeError(w, http.StatusBadRequest, "bad_request", "this list does not support the active filter")
			return l, false
		}
		switch v {
		case "1", "true":
			t := true
			l.Active = &t
		case "0", "false":
			f := false
			l.Active = &f
		default:
			writeError(w, http.StatusBadRequest, "bad_request", "active must be true or false")
			return l, false
		}
	}
	return l, true
}

// setTotal publishes the filtered row count behind a list response.
func setTotal(w http.ResponseWriter, total int) {
	w.Header().Set("X-Total-Count", strconv.Itoa(total))
}

func containsStr(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, ", ")
}
