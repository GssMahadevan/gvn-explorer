# Top-level entry points. The specifics live in makefiles/, one file per
# concern (same layout as the gvndb sample):
#   makefiles/build.mk   - server binary + GUI bundle
#   makefiles/test.mk    - go tests, curl API suite, TLS smoke, GUI walk
#   makefiles/deploy.mk  - docker image + compose lifecycle
#   makefiles/demo.mk    - narrated demo videos (docs/demo-*.mp4)

-include makefiles/build.mk
-include makefiles/test.mk
-include makefiles/deploy.mk
-include makefiles/demo.mk

.DEFAULT_GOAL := help

help:
	@echo "build        server binary + GUI bundle"
	@echo "dist         cross-compiled release bundles (linux/windows/macOS, amd64+arm64) in dist/"
	@echo "test-dist    build dist + smoke-test this machine's bundle (embedded GUI, zero flags)"
	@echo "test         go vet/test + curl API suite + TLS smoke + GUI walk (needs nothing running)"
	@echo "docker-build build the container image"
	@echo "docker-push  publish multi-arch image (IMAGE=<registry>/name:tag)"
	@echo "docker-up    build + start via compose (app on :8080)"
	@echo "docker-down  stop compose"
	@echo "demo         rebuild the narrated demo videos (docs/demo-*.mp4)"
.PHONY: help
