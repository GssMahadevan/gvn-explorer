# Test entry points. All are self-contained: test-api starts a throwaway
# server on its own port with a temp db/root and tears it down.
export GO111MODULE=on

API_PORT ?= 8097

test: test-go test-api test-gui
.PHONY: test test-go test-api test-gui

test-go:
	go vet ./...
	go test ./...

# Full curl API regression (323 cases) against a disposable instance.
test-api: build-server
	@dir=$$(mktemp -d); \
	./bin/server -addr :$(API_PORT) -root $$dir/root -db $$dir/perms.db \
		-admin system:admin:admin >$$dir/server.log 2>&1 & pid=$$!; \
	sleep 0.7; \
	BASE_URL=http://localhost:$(API_PORT) ADMIN_TENANT=system ADMIN_USER=admin ADMIN_PASS=admin \
		./scripts/test_files.sh; rc=$$?; \
	kill $$pid 2>/dev/null; rm -rf $$dir; exit $$rc

# Headless-Chrome GUI walk (scripts/test_gui.sh builds its own
# fixtures/server on :8096; needs local Chrome).
test-gui:
	./scripts/test_gui.sh
