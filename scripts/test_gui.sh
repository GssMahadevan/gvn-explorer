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
step "go build bin/server" env GO111MODULE=on go build -o bin/server ./cmd/server
if [ ! -d frontend/dist ]; then
  step "frontend build" sh -c 'npm --prefix frontend ci --silent && npm --prefix frontend run build'
fi
if [ ! -d scripts/gui/node_modules/puppeteer-core ]; then
  step "npm install (scripts/gui)" npm --prefix scripts/gui install --silent
fi
[ -x bin/server ] && [ -d frontend/dist ] && [ -d scripts/gui/node_modules/puppeteer-core ] || {
  echo "build prerequisites missing; cannot run GUI walk"; exit 2;
}

# ---------- throwaway sandbox + fixture ----------
WORK=$(mktemp -d /tmp/fx-gui-run.XXXXXX)
mkdir -p "$WORK/root/deliverables" "$WORK/root/docs"
printf 'PDF-1.4 fake quarterly report\n' > "$WORK/root/deliverables/report-q3.pdf"
printf '# readme\nhello\n' > "$WORK/root/readme.md"
printf 'release notes\n' > "$WORK/root/docs/notes.md"

echo "== boot server :$PORT (root + db in $WORK) =="
"$PWD/bin/server" -addr ":$PORT" -root "$WORK/root" -db "$WORK/perms.db" \
  -admin system:admin:secret -web "$PWD/frontend/dist" > "$WORK/server.log" 2>&1 &
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
