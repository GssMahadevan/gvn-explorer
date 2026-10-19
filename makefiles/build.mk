# Local builds. GO111MODULE is sometimes "off" in operator shells; this
# repo is a module, so pin it on for every recipe here.
export GO111MODULE=on

build: build-server build-web
.PHONY: build build-server build-web web-deps

build-server:
	go build -trimpath -ldflags='-s -w' -o bin/server ./cmd/server

# GUI bundle -> frontend/dist (what -web / the container image serve).
web-deps:
	npm --prefix frontend ci

build-web:
	npm --prefix frontend run build
