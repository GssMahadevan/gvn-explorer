#!/usr/bin/env bash
# Release-bundle smoke test. Detects *this* machine's os/arch, unpacks the
# matching dist/bundles archive into a temp dir, starts it with ZERO flags,
# and checks the embedded GUI and /help really are served from the binary.
# Intended entry point: make test-dist  (builds dist first).
set -u
cd "$(dirname "$0")/.."
DIST="${DIST:-dist}"
OS="${OS:-$(uname -s | tr '[:upper:]' '[:lower:]')}" # darwin | linux
ARCH="${ARCH:-$(uname -m)}"
case $ARCH in x86_64) ARCH=amd64 ;; aarch64) ARCH=arm64 ;; esac
# OS=/ARCH= can be overridden to test another bundle — e.g. the Intel
# Mac binary on Apple Silicon runs under Rosetta 2:
#   ARCH=amd64 make test-dist   (windows bundles are not runnable here)

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

# Zero flags now means HTTPS with a self-signed cert — the smoke proves
# the default works: https answers, and plain http gets the 308 redirect.
ADDR=":${PORT:-8123}" "./$BUNDLE" >server.log 2>&1 &
SRV=$!
for _ in $(seq 1 40); do
  curl -skf "https://localhost:${PORT:-8123}/healthz" >/dev/null 2>&1 && break
  sleep 0.25
done

curl -skf "https://localhost:${PORT:-8123}/healthz" >/dev/null; ck "unpack-and-run serves /healthz over https with zero flags (self-signed default)" $?
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://localhost:${PORT:-8123}/healthz")" = 308 ]; ck "plain http on the TLS port answers 308 -> https" $?
curl -skf "https://localhost:${PORT:-8123}/" | grep -qi '<title>'; ck "embedded GUI is served at /" $?
grep -q '(embedded)' server.log; ck "server log reports the GUI came from the binary" $?
curl -sk -c j -X POST "https://localhost:${PORT:-8123}/api/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d '{"tenant":"system","username":"admin","password":"admin"}' >/dev/null
curl -skf -b j "https://localhost:${PORT:-8123}/api/v1/fs?path=/help" | grep -q 'README.md'
ck "embedded /help docs reachable through the API" $?

echo "dist smoke: passed=$PASS failed=$FAIL"
[ $FAIL = 0 ]
