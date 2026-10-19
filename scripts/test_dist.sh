#!/usr/bin/env bash
# Release-bundle smoke test. Detects *this* machine's os/arch, unpacks the
# matching dist/bundles archive into a temp dir, starts it with ZERO flags,
# and checks the embedded GUI and /help really are served from the binary.
# Intended entry point: make test-dist  (builds dist first).
set -u
cd "$(dirname "$0")/.."
DIST="${DIST:-dist}"
OS=$(uname -s | tr '[:upper:]' '[:lower:]') # darwin | linux
ARCH=$(uname -m)
case $ARCH in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac

PASS=0
FAIL=0
ck() { # ck <name> <exit-code-of-checked-command>
  if [ "$2" = 0 ]; then PASS=$((PASS + 1)); echo "  PASS $1"; else FAIL=$((FAIL + 1)); echo "  FAIL $1"; fi
}

ARCHIVE=$(ls "$DIST"/bundles/gvn-explorer-*-"$OS"-"$ARCH".tar.gz "$DIST"/bundles/gvn-explorer-*-"$OS"-"$ARCH".zip 2>/dev/null | head -1)
if [ -z "$ARCHIVE" ]; then
  echo "no $OS/$ARCH bundle under $DIST/bundles — run: make dist"
  exit 1
fi
echo "== dist smoke: $(basename "$ARCHIVE") =="

D=$(mktemp -d)
SRV=""
cleanup() { [ -n "$SRV" ] && kill "$SRV" 2>/dev/null; rm -rf "$D"; }
trap cleanup EXIT

cd "$D"
case $ARCHIVE in
*.zip) unzip -q "$OLDPWD/$ARCHIVE" ;;
*) tar xzf "$OLDPWD/$ARCHIVE" ;;
 esac
BUNDLE=$(ls -d gvn-explorer-*)
cd "$BUNDLE"

# the whole point: nothing but the binary + README travels
[ ! -d web ]; ck "bundle ships no web/ dir (GUI must be embedded)" $?
[ -f README.md ]; ck "getting-started README present" $?

ADDR=":${PORT:-8123}" "./$BUNDLE" >server.log 2>&1 &
SRV=$!
for _ in $(seq 1 40); do
  curl -sf "localhost:${PORT:-8123}/healthz" >/dev/null 2>&1 && break
  sleep 0.25
done

curl -sf "localhost:${PORT:-8123}/healthz" >/dev/null; ck "unpack-and-run serves /healthz with zero flags" $?
curl -sf "localhost:${PORT:-8123}/" | grep -qi '<title>'; ck "embedded GUI is served at /" $?
grep -q '(embedded)' server.log; ck "server log reports the GUI came from the binary" $?
curl -s -c j -X POST "localhost:${PORT:-8123}/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"system","username":"admin","password":"admin"}' >/dev/null
curl -sf -b j "localhost:${PORT:-8123}/api/v1/fs?path=/help" | grep -q 'README.md'
ck "embedded /help docs reachable through the API" $?

echo "dist smoke: passed=$PASS failed=$FAIL"
[ $FAIL = 0 ]
