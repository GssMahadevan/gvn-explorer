# Test entry points. All are self-contained: test-api starts a throwaway
# server on its own port with a temp db/root and tears it down.
export GO111MODULE=on

API_PORT ?= 8097
MATRIX_PORT ?= 8104
PUREGO_PORT ?= 8105

test: test-go test-api test-tls test-matrix test-matrix-purego test-gui
.PHONY: test test-go test-api test-tls test-matrix test-matrix-purego test-gui

test-go:
	go vet ./...
	go test ./...
	go vet -tags sqlite_purego ./...
	go test -tags sqlite_purego ./...

# Full curl API regression (350 cases) against a disposable instance.
test-api: build-server
	@dir=$$(mktemp -d); \
	./bin/server -addr :$(API_PORT) -root $$dir/root -db $$dir/perms.db -tls-self=false \
		-admin system:admin:admin >$$dir/server.log 2>&1 & pid=$$!; \
	sleep 0.7; \
	BASE_URL=http://localhost:$(API_PORT) ADMIN_TENANT=system ADMIN_USER=admin ADMIN_PASS=admin \
		./scripts/test_files.sh; rc=$$?; \
	kill $$pid 2>/dev/null; rm -rf $$dir; exit $$rc

# HTTPS smoke (scripts/test-tls.sh on :8099/:8100): self-signed -tls-self,
# file certs via scripts/gen-selfsigned.sh, HTTP->HTTPS 308, Secure cookies,
# security headers.
test-tls: build-server
	@./scripts/test-tls.sh

# Cross-tenant attack matrix, within-tenant r/w/d permission matrix and
# hostile-path fuzz (scripts/test_matrix.py) against a disposable instance.
test-matrix: build-server
	@dir=$$(mktemp -d); \
	./bin/server -addr :$(MATRIX_PORT) -root $$dir/root -db $$dir/perms.db -tls-self=false \
		-admin system:admin:admin >$$dir/server.log 2>&1 & pid=$$!; \
	sleep 0.7; \
	python3 ./scripts/test_matrix.py --base-url http://localhost:$(MATRIX_PORT) \
		--admin-tenant system --admin-user admin --admin-pass admin; rc=$$?; \
	kill $$pid 2>/dev/null; rm -rf $$dir; exit $$rc

# The same matrix against the -tags sqlite_purego build (the ncruces
# arch-independent driver used for the MIPS release binaries): proves the
# swapped driver is behaviourally identical on the host.
test-matrix-purego:
	@go build -trimpath -tags sqlite_purego -o bin/server-purego ./cmd/server
	@dir=$$(mktemp -d); \
	./bin/server-purego -addr :$(PUREGO_PORT) -root $$dir/root -db $$dir/perms.db -tls-self=false \
		-admin system:admin:admin >$$dir/server.log 2>&1 & pid=$$!; \
	sleep 0.7; \
	python3 ./scripts/test_matrix.py --base-url http://localhost:$(PUREGO_PORT) \
		--admin-tenant system --admin-user admin --admin-pass admin; rc=$$?; \
	kill $$pid 2>/dev/null; rm -rf $$dir; exit $$rc

# Headless-Chrome GUI walk (scripts/test_gui.sh builds its own
# fixtures/server on :8096; needs local Chrome).
test-gui:
	./scripts/test_gui.sh
