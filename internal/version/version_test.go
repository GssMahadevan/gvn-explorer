package version

import "testing"

func TestGetDerivesHashFromDescribe(t *testing.T) {
	oldV, oldH := Version, GitHash
	defer func() { Version, GitHash = oldV, oldH }()

	// Makefile stamps both: Get must pass them through untouched.
	Version, GitHash = "v0.1.0-42-gad9832d", "ad9832d"
	if got := Get(); got.Version != "v0.1.0-42-gad9832d" || got.GitHash != "ad9832d" {
		t.Fatalf("stamped passthrough broken: %+v", got)
	}

	// Only Version stamped (e.g. someone hand-rolled one -X flag):
	// the hash suffix of describe output is extracted, not invented.
	Version, GitHash = "v0.1.0-42-gbeef42-dirty", ""
	if got := Get(); got.GitHash != "beef42-dirty" {
		t.Fatalf("derive from describe suffix: got %q", got.GitHash)
	}

	// Unstamped build reports honestly.
	Version, GitHash = "dev", ""
	if got := Get(); got.Version != "dev" || got.GitHash != "" {
		t.Fatalf("dev build should say dev: %+v", got)
	}
}
