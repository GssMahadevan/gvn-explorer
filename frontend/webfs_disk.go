//go:build !embedweb

// Package frontend connects the built GUI bundle to the server. The
// default build serves the GUI only from disk (-web / WEB_DIR), exactly
// as before. Release builds (make dist) compile with -tags embedweb,
// which swaps in frontend/webfs_embed.go and bakes the Vite output
// (frontend/dist) into the binary — release binaries then need no web/
// folder. The Docker image deliberately stays on the disk-served GUI
// (/app/web), which keeps the bundle swappable as an image layer.
package frontend

import "io/fs"

// BundleFS returns the embedded GUI bundle, or nil when this binary was
// built without -tags embedweb.
func BundleFS() fs.FS { return nil }
