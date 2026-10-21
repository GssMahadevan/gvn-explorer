// Package version carries build identity, stamped at link time by the
// Makefile (see makefiles/build.mk): `go build -ldflags
// "-X .../internal/version.Version=$(git describe --tags --always)"`.
// A plain `go build` leaves the zero values — dev builds say "dev",
// and the GUI About box shows that honestly instead of lying.
//
// Version is git-describe output: the closest tag, the number of
// commits since it, and the hash (v0.1.0-42-gad9832d). The commit
// count is the meaningful sequence number behind the hash: it orders
// any two builds and says how far each moved from a published release.
package version

import "strings"

// stamped by -X flags; the zero values are meaningful ("an unstamped
// build"), so no code below initializes them differently.
var (
	Version = "dev"
	GitHash = ""
)

// Info is the wire shape served at GET /api/v1/version (and shown in
// the GUI About box).
type Info struct {
	Version string `json:"version"`
	GitHash string `json:"git_hash"`
}

func Get() Info {
	hash := GitHash
	if hash == "" && Version != "dev" {
		// describe output ends in -g<hash>: surface it separately too
		// so consumers (scripts, the About box) never have to parse,
		// even if only Version was stamped.
		if i := strings.LastIndex(Version, "-g"); i >= 0 && i+2 < len(Version) {
			hash = Version[i+2:]
		}
	}
	return Info{Version: Version, GitHash: hash}
}
