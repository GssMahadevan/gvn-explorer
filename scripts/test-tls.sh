#!/usr/bin/env bash
# test-tls.sh — TLS smoke test, the counterpart of test_files.sh for the
# HTTPS path (the API contract itself is unchanged there, and
# test_files.sh keeps covering the opted-out plain-HTTP listener).
#
# Scenario 1 — zero flags (HTTPS is the default, self-signed cert):
#   * https /healthz answers
#   * the cert is genuinely self-signed (curl without -k refuses it)
#   * plain HTTP on the TLS port gets 308 + https Location (GET and POST)
#   * the session cookie carries Secure when served over TLS
# Scenario 2 — scripts/gen-selfsigned.sh + -tls-cert/-tls-key:
#   * the generated cert verifies with curl --cacert (no -k)
#   * security headers (CSP, nosniff, frame, referrer) are present
# Scenario 3 — -tls-self=false opt-out:
#   * plain HTTP answers directly, cookie has no Secure attribute
#
# Self-contained: own ports, temp db/root, always cleaned up.
# Ports overridable: TLS_PORT=… TLS_PORT2=… TLS_PORT3=…
set -uo pipefail
cd "$(dirname "$0")/.."

BIN=${BIN:-./bin/server}
P1=${TLS_PORT:-8099}
P2=${TLS_PORT2:-8100}
P3=${TLS_PORT3:-8101}
DIR=$(mktemp -d)
SRV=""
rc=0

fail() { echo "FAIL: $1"; rc=1; }
ok()   { echo "ok:   $1"; }
stop() {
  if [ -n "$SRV" ]; then
    kill "$SRV" 2>/dev/null; wait "$SRV" 2>/dev/null; SRV=""
    sleep 0.3
  fi
}
trap 'stop; rm -rf "$DIR"' EXIT

[ -x "$BIN" ] || { echo "no $BIN — build it first (make build-server)" >&2; exit 1; }

echo "== scenario 1: zero flags — HTTPS is the default (self-signed) on :$P1"
"$BIN" -addr 127.0.0.1:$P1 -root "$DIR/root" -db "$DIR/perms.db" >/dev/null 2>&1 &
SRV=$!
sleep 0.7

body=$(curl -skf "https://127.0.0.1:$P1/healthz" || true)
case "$body" in *'"status":"ok"'*) ok "https /healthz answers over the self-signed cert";; *) fail "https /healthz: '$body'";; esac

err=$(curl -sS -o /dev/null "https://127.0.0.1:$P1/healthz" 2>&1 || true)
echo "$err" | grep -qi 'self[- ]signed' \
  && ok "curl without -k refuses it (cert really is untrusted/self-signed)" \
  || fail "expected a trust error, got: $err"

code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$P1/healthz")
loc=$(curl -sI "http://127.0.0.1:$P1/api/v1/fs?path=/" | tr -d '\r' | awk -F': ' 'tolower($1)=="location"{print $2}')
if [ "$code" = 308 ] && [ "$loc" = "https://127.0.0.1:$P1/api/v1/fs?path=/" ]; then
  ok "plain HTTP on the TLS port -> 308 with https Location"
else
  fail "http redirect: code=$code location=$loc"
fi

code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "http://127.0.0.1:$P1/api/v1/auth/login")
[ "$code" = 308 ] && ok "plain HTTP POST -> 308 too (308 preserves method+body)" \
  || fail "POST redirect gave $code"

hdr=$(curl -sk -D - -o /dev/null -H 'Content-Type: application/json' \
  -d '{"tenant":"system","username":"admin","password":"admin"}' \
  "https://127.0.0.1:$P1/api/v1/auth/login")
echo "$hdr" | grep -i '^set-cookie: sid=[^;]*' | grep -qi 'secure' \
  && ok "session cookie carries Secure over TLS (cookie-secure=auto)" \
  || fail "login response lacks a Secure cookie: $hdr"
stop

echo "== scenario 2: gen-selfsigned.sh + -tls-cert/-tls-key on :$P2"
./scripts/gen-selfsigned.sh "$DIR/tls" >/dev/null \
  || { fail "gen-selfsigned.sh"; exit 1; }
"$BIN" -addr 127.0.0.1:$P2 -root "$DIR/root" -db "$DIR/perms.db" \
  -tls-cert "$DIR/tls/server.crt" -tls-key "$DIR/tls/server.key" >/dev/null 2>&1 &
SRV=$!
sleep 0.7

body=$(curl -sf --cacert "$DIR/tls/server.crt" "https://localhost:$P2/healthz" || true)
case "$body" in *'"status":"ok"'*) ok "generated cert verifies with curl --cacert (no -k)";; *) fail "verified fetch: '$body'";; esac

hdr=$(curl -sk -D - -o /dev/null "https://localhost:$P2/healthz")
for h in 'x-content-type-options: nosniff' 'x-frame-options: DENY' 'referrer-policy: no-referrer' 'content-security-policy: default-src'; do
  echo "$hdr" | grep -qi "$h" && ok "response carries $h" || fail "missing header: $h"
done
stop

echo "== scenario 3: -tls-self=false — plain HTTP opt-out on :$P3"
"$BIN" -addr 127.0.0.1:$P3 -root "$DIR/root" -db "$DIR/perms.db" \
  -tls-self=false >/dev/null 2>&1 &
SRV=$!
sleep 0.7

body=$(curl -sf "http://127.0.0.1:$P3/healthz" || true)
case "$body" in *'"status":"ok"'*) ok "plain HTTP answers directly (no TLS, no redirect)";; *) fail "plain http /healthz: '$body'";; esac

hdr=$(curl -s -D - -o /dev/null -H 'Content-Type: application/json' \
  -d '{"tenant":"system","username":"admin","password":"admin"}' \
  "http://127.0.0.1:$P3/api/v1/auth/login")
echo "$hdr" | grep -i '^set-cookie: sid=' | grep -qi 'secure' \
  && fail "cookie should NOT claim Secure over plain HTTP" \
  || ok "cookie correctly omits Secure over plain HTTP"
stop

if [ $rc -eq 0 ]; then echo "PASS: all TLS checks"; else echo "FAIL: TLS smoke test"; fi
exit $rc
