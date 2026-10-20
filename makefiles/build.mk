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
# One self-contained file per OS/arch: the server binary built with
# -tags embedweb, which bakes the GUI bundle (frontend/webfs_embed.go)
# and the /help docs (docs/embed.go) into the binary. A bundle really
# is just binary + README; -web still overrides with disk files.
# Attach the archives to GitHub Releases; VERSION defaults to the
# current commit so artifacts can't collide silently.
DIST ?= dist
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
DIST_PLATFORMS := linux/amd64 linux/arm64 linux/mips linux/mipsle windows/amd64 windows/arm64 darwin/amd64 darwin/arm64

# 32-bit MIPS (the common OpenWrt router targets) needs two extra knobs:
# GOMIPS=softfloat (those SoCs have no FPU) and -tags sqlite_purego
# (modernc.org/sqlite has no 32-bit-MIPS generated code; see
# internal/db/driver_purego.go).
dist: build-web
	@rm -rf $(DIST)
	@fail=0; for p in $(DIST_PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=; \
	  [ "$$os" = windows ] && ext=.exe; \
	  name=gvn-explorer-$(VERSION)-$$os-$$arch; \
	  echo "==> $$os/$$arch"; \
	  extra=; tags=embedweb; \
	  case $$arch in mips|mipsle) extra=GOMIPS=softfloat; tags=embedweb,sqlite_purego;; esac; \
	  mkdir -p $(DIST)/bundles/$$name; \
	  env $$extra GOOS=$$os GOARCH=$$arch CGO_ENABLED=0 go build -trimpath -tags "$$tags" -ldflags='-s -w' -o $(DIST)/bundles/$$name/$$name$$ext ./cmd/server || fail=1; \
	  cp deployments/binary/README.md $(DIST)/bundles/$$name/README.md; \
	  ( cd $(DIST)/bundles && if [ "$$os" = windows ]; then zip -qr $$name.zip $$name; else tar czf $$name.tar.gz $$name; fi ) || fail=1; \
	  cp $(DIST)/bundles/$$name/$$name$$ext $(DIST)/$$name$$ext; \
	done; \
	[ $$fail = 0 ] && echo "archives in $(DIST)/bundles/, bare binaries in $(DIST)/" && ls -1 $(DIST)/bundles | grep -E '\.(zip|tar\.gz)$$'

# Smoke-test the bundle for *this* machine: unpack, run with zero flags,
# check the embedded GUI and /help are served (scripts/test_dist.sh).
test-dist: dist
	./scripts/test_dist.sh
.PHONY: dist test-dist

# ---- Android APK (an old phone as a home server) -------------------------
# The app in android/ is a thin wrapper: a foreground service that exec()s
# the ordinary CGO_ENABLED=0 server binary. It ships in jniLibs named
# lib*.so so the installer drops it into nativeLibraryDir — the one place
# a modern targetSdk still lets an app exec from (same trick as
# syncthing-android). arm64 uses GOOS=android; plain GOOS=android/arm
# would demand cgo, but a static linux/arm binary runs on Android's
# kernel as-is, so 32-bit (and the emulator's x86_64) use GOOS=linux.
ANDROID_JNILIBS := android/app/src/main/jniLibs
ANDROID_HOME ?= $(HOME)/Library/Android/sdk
# brew gradle 7.5 runs on JDK 17 (it refuses 19); pick 17 if present.
GRADLE_JAVA ?= $(shell /usr/libexec/java_home -v 17 2>/dev/null || echo "$(JAVA_HOME)")

android-so: build-web
	@for spec in "arm64-v8a android arm64 " "armeabi-v7a linux arm GOARM=7" "x86_64 linux amd64 "; do \
	  set -- $$spec; abi=$$1; goos=$$2; goarch=$$3; goarm=$$4; \
	  echo "==> jniLibs/$$abi"; \
	  mkdir -p $(ANDROID_JNILIBS)/$$abi; \
	  env $$goarm GOOS=$$goos GOARCH=$$goarch CGO_ENABLED=0 go build -trimpath -tags embedweb -ldflags='-s -w' -o $(ANDROID_JNILIBS)/$$abi/libgvnserver.so ./cmd/server || exit 1; \
	done

android-apk: android-so
	@ANDROID_HOME=$(ANDROID_HOME) JAVA_HOME=$(GRADLE_JAVA) ./android/gradlew -p android assembleDebug
	@mkdir -p $(DIST)
	@cp android/app/build/outputs/apk/debug/app-debug.apk $(DIST)/gvn-explorer-$(VERSION)-android.apk
	@echo "APK: $(DIST)/gvn-explorer-$(VERSION)-android.apk"
.PHONY: android-so android-apk
