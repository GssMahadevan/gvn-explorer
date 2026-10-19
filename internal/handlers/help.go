package handlers

import (
	"net/http"
	"strings"
)

// The built-in user guide (docs/user/, embedded into the binary) is
// exposed through the file API as the reserved /help tree: every
// logged-in caller can read it — no grants, no tenant scoping — and
// nothing in it can be written. A root directory actually named "help"
// is shadowed by it; that trade-off is documented in the READMEs.

const helpPrefix = "/help"

// helpSub maps a logical path to a relative path inside the embedded
// FS: "/help" -> "." (tree root), "/help/a/b.md" -> "a/b.md".
func helpSub(p string) (string, bool) {
	if p == helpPrefix {
		return ".", true
	}
	if sub := strings.TrimPrefix(p, helpPrefix+"/"); sub != p && sub != "" {
		return sub, true
	}
	return "", false
}

func isHelpPath(p string) bool { _, ok := helpSub(p); return ok }

// helpReadOnly is the stable rejection every mutating call gets on /help.
func helpReadOnly(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "read_only", "the built-in /help docs tree is read-only")
}
