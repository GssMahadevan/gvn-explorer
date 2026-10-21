#!/usr/bin/env bash
# GUI integration test: builds the server + UI bundle if needed, boots a real
# server against a throwaway sandbox root with seeded fixture data, then runs
# the headless-Chrome walk (scripts/gui/walk.js) against it.
#
# Usage: ./scripts/test_gui.sh            # build + boot + walk
#        SHOTS=1 ./scripts/test_gui.sh    # also save light/dark screenshots
# Env overrides: PORT (8096), OUT_DIR (/tmp/fx-gui), CHROME_PATH.
# Needs: go, node/npm, Google Chrome. Node deps install themselves on first run.
set -u
cd "$(cd "$(dirname "$0")/.." && pwd)"

PORT="${PORT:-8096}"
BASE_URL="http://localhost:$PORT"
OUT_DIR="${OUT_DIR:-/tmp/fx-gui}"
CHROME="${CHROME_PATH:-/Applications/Google Chrome.app/Contents/MacOS/Google Chrome}"
PASS=0
FAIL=0

step() { # step <name> <cmd...> — run, show output indented, count pass/fail
  local name=$1; shift
  if "$@"; then
    PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %s\n' "$name"
  else
    FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %s\n' "$name"
  fi
}

[ -x "$CHROME" ] || { echo "Chrome not found at $CHROME (set CHROME_PATH)"; exit 2; }

# ---------- build ----------
echo "== build =="
# Stamp build identity like make does: the walk asserts the About box
# shows exactly what the binary reports, so this must not be a plain
# unstamped build.
GVN_DESC=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
GVN_HASH=$(git rev-parse --short HEAD 2>/dev/null || echo dev)
GVN_VPKG=github.com/GssMahadevan/gvn-explorer/internal/version
step "go build bin/server" env GO111MODULE=on go build -ldflags="-X ${GVN_VPKG}.Version=${GVN_DESC} -X ${GVN_VPKG}.GitHash=${GVN_HASH}" -o bin/server ./cmd/server
if [ ! -d frontend/node_modules ]; then
  step "npm ci (frontend)" npm --prefix frontend ci --silent
fi
step "frontend build" npm --prefix frontend run build
if [ ! -d scripts/gui/node_modules/puppeteer-core ] || [ ! -d scripts/gui/node_modules/jsqr ]; then
  step "npm install (scripts/gui)" npm --prefix scripts/gui install --silent
fi
[ -x bin/server ] && [ -d frontend/dist ] && [ -d scripts/gui/node_modules/puppeteer-core ] || {
  echo "build prerequisites missing; cannot run GUI walk"; exit 2;
}

# ---------- throwaway sandbox + fixture ----------
WORK=$(mktemp -d /tmp/fx-gui-run.XXXXXX)
mkdir -p "$WORK/root/deliverables" "$WORK/root/docs" "$WORK/root/photos" "$WORK/root/videos" "$WORK/root/audios"
# real one-page PDF (generated once, committed) — the walk asserts the
# file page embeds it in the browser's own pdf viewer via ?dispo=inline
cp scripts/gui/fixtures/report-q3.pdf "$WORK/root/deliverables/report-q3.pdf"
# real zip: stays download-only (no browser-native viewer) — proves the
# download path survives the media allowlist
cp scripts/gui/fixtures/data.zip "$WORK/root/deliverables/data.zip"
printf '# readme\nhello\n' > "$WORK/root/readme.md"
printf 'release notes\n' > "$WORK/root/docs/notes.md"
# media fixtures: real 8x8 PNG and a real 1s H.264 MP4 (both generated once
# with ffmpeg and committed under scripts/gui/fixtures/) — the walk asserts
# the browser actually loads the image and decodes/plays the video.
cp scripts/gui/fixtures/pic.png "$WORK/root/photos/pic.png"
cp scripts/gui/fixtures/clip.mp4 "$WORK/root/videos/clip.mp4"
# 0.3 s PCM wav — the walk asserts the file page gets the native
# <audio controls> and the browser decodes the stream
cp scripts/gui/fixtures/chime.wav "$WORK/root/audios/chime.wav"
# YAML fixture for the structured tree editor: leading comment, inline
# comment, block scalar and two documents must all survive the
# tree-edit -> draft -> publish round trip.
cat > "$WORK/root/walk-cfg.yml" <<'YAML'
# site process config
app: web1 # do not touch
replicas: 2
env:
  - name: LOG_LEVEL # level
    value: debug
notes: |
  why this box exists
  owned by team-a
---
agent: a1
YAML

echo "== boot server :$PORT (root + db in $WORK) =="
"$PWD/bin/server" -addr ":$PORT" -root "$WORK/root" -db "$WORK/perms.db" \
  -admin system:admin:secret -web "$PWD/frontend/dist" -tls-self=false > "$WORK/server.log" 2>&1 &
SRV=$!
trap 'kill $SRV 2>/dev/null; rm -rf "$WORK"' EXIT
for _ in $(seq 1 40); do curl -s -o /dev/null "$BASE_URL/api/v1/me" && break; sleep 0.25; done
curl -s -o /dev/null "$BASE_URL/api/v1/me" || { echo "server did not come up:"; sed 's/^/  /' "$WORK/server.log"; exit 2; }

CJ="$WORK/cj"
api() { curl -s -b "$CJ" -c "$CJ" -H 'Content-Type: application/json' "$@"; }
echo "== seed fixtures (acme/globex tenants, users incl. one suspended) =="
api -d '{"tenant":"system","username":"admin","password":"secret"}' "$BASE_URL/api/v1/auth/login" > /dev/null
for spec in "acme:ACME Corp" "globex:Globex Corp" "home1:home lab"; do
  api -X POST -d "{\"name\":\"${spec%%:*}\",\"description\":\"${spec#*:}\"}" "$BASE_URL/api/v1/tenants" > /dev/null
done
mk_user() { # mk_user <tenant> <user> <pass> <description>
  api -X POST -d "{\"tenant\":\"$1\",\"username\":\"$2\",\"password\":\"$3\",\"description\":\"$4\"}" "$BASE_URL/api/v1/users"
}
mk_user acme alice 'pw-alice-1' 'acme engineer' > /dev/null
BOB=$(mk_user acme bob 'pw-bob-1' 'deliberately suspended demo account' \
  | python3 -c 'import sys,json; print(json.load(sys.stdin).get("id",""))' 2>/dev/null)
[ -n "$BOB" ] && api -X POST -d '{"active":false}' "$BASE_URL/api/v1/users/$BOB/active" > /dev/null
mk_user globex carol 'pw-carol-1' 'globex admin' > /dev/null

# ---------- drive the browser ----------
echo "== walk.js ($BASE_URL) =="
step "GUI walk" env BASE_URL="$BASE_URL" CHROME_PATH="$CHROME" OUT_DIR="$OUT_DIR" node scripts/gui/walk.js

if [ "${SHOTS:-0}" = "1" ]; then
  echo "== shot.js =="
  step "screenshots" env BASE_URL="$BASE_URL" CHROME_PATH="$CHROME" OUT_DIR="$OUT_DIR" node scripts/gui/shot.js
  echo "screenshots in $OUT_DIR"
fi

echo
echo "setup+walk passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
