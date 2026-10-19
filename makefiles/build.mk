# Local builds + cross-compiled release artifacts. GO111MODULE is
# sometimes "off" in operator shells; this repo is a module, so pin it
# on for every recipe here.
export GO111MODULE=on

build: build-server build-web
.PHONY: build build-server build-web web-deps dist

build-server:
	go build -trimpath -ldflags='-s -w' -o bin/server ./cmd/server

# GUI bundle -> frontend/dist (what -web / the container image serve).
web-deps:
	npm --prefix frontend ci

build-web:
	npm --prefix frontend run build

# ---- release artifacts ----------------------------------------------------
# One self-contained bundle per OS/arch under dist/: server binary + the
# GUI bundle it serves from ./web + a getting-started README, zipped.
# The embedded docs are inside the binary (docs/embed.go), so nothing
# else has to travel. Attach the archives to GitHub Releases; VERSION
# defaults to the current commit so artifacts can't collide silently.
DIST ?= dist
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
DIST_PLATFORMS := linux/amd64 linux/arm64 windows/amd64 windows/arm64 darwin/amd64 darwin/arm64

dist: build-web
	@rm -rf $(DIST)
	@fail=0; for p in $(DIST_PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; \
	  [ "$$os" = windows ] && ext=.exe; \
	  name=gvn-explorer-$(VERSION)-$$os-$$arch; \
	  echo "==> $$os/$$arch"; \
	  mkdir -p $(DIST)/bundles/$$name; \
	  GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags='-s -w' -o $(DIST)/bundles/$$name/$$name$$ext ./cmd/server || fail=1; \
	  cp deployments/binary/README.md $(DIST)/bundles/$$name/README.md; \
	  cp -R frontend/dist $(DIST)/bundles/$$name/web; \
	  ( cd $(DIST)/bundles && if [ "$$os" = windows ]; then zip -qr $$name.zip $$name; else tar czf $$name.tar.gz $$name; fi ) || fail=1; \
	  cp $(DIST)/bundles/$$name/$$name$$ext $(DIST)/$$name$$ext; \
	done; \
	[ $$fail = 0 ] && echo "archives in $(DIST)/bundles/, bare binaries in $(DIST)/" && ls -1 $(DIST)/bundles | grep -E '\.(zip|tar\.gz)$$'
