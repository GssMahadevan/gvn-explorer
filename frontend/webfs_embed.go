//go:build embedweb

// Embedded-GUI variant, selected by -tags embedweb (used by make dist).
// go:embed reads paths relative to this package directory, so it picks
// up the real Vite output directly — one source of truth, no copy step.
// Building with this tag requires `make build-web` to have run first
// (frontend/dist is generated, not checked in; go build says so loudly).
package frontend

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var bundleFS embed.FS

// BundleFS returns the GUI bundle baked into this binary.
func BundleFS() fs.FS {
	sub, err := fs.Sub(bundleFS, "dist")
	if err != nil {
		panic("frontend: embedded dist missing: " + err.Error())
	}
	return sub
}
