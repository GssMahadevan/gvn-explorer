// Package docs embeds the end-user guide set (docs/user/) into the
// binary. Like the web bundle, the docs travel with the server: any
// deployment — docker or plain binary — can serve them, and they are
// exposed through the API as the read-only /help tree (see
// internal/handlers/help.go). Keep the content in sync with the repo
// README when the API contract changes.
package docs

import (
	"embed"
	"io/fs"
)

//go:embed all:user
var userFS embed.FS

// FS returns the embedded user-guide tree rooted at the folder's top
// level ("README.md", "permissions.md", ...). It never returns nil:
// a missing embedded subfolder is a build-time error, not a runtime one.
func FS() fs.FS {
	sub, err := fs.Sub(userFS, "user")
	if err != nil {
		panic("docs: embedded user/ tree missing: " + err.Error())
	}
	return sub
}
