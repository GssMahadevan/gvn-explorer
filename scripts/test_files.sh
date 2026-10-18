#!/usr/bin/env bash
# Regression tests for the file-explorer API (auth + ACLs + fs ops).
# Usage: BASE_URL=http://localhost:8095 ADMIN_USER=admin ADMIN_PASS=secret ./scripts/test_files.sh
# Creates throwaway users/groups/paths with a random suffix; safe to re-run.
set -u
BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin}"
SFX=$RANDOM
AJ=/tmp/fx-admin-$$.jar   # admin cookie jar
UJ=/tmp/fx-user-$$.jar    # regular user cookie jar
USER="u$SFX"
GROUP="g$SFX"
PASS=0 FAIL=0

cleanup() { rm -f "$AJ" "$UJ"; }
trap cleanup EXIT

t() { # t <jar|-> <name> <want_status> <METHOD> <path> [json-body]
  local jar=$1 name=$2 want=$3 method=$4 path=$5 body=${6:-}
  local args=(-s -o /tmp/resp.json -w '%{http_code}' -X "$method" "$BASE_URL$path")
  [ "$jar" != "-" ] && args+=(-b "$jar" -c "$jar")
  [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
  local got; got=$(curl "${args[@]}")
  if [ "$got" = "$want" ]; then
    PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-42s %s %s -> %s\n' "$name" "$method" "$path" "$got"
  else
    FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-42s %s %s -> got %s want %s\n' "$name" "$method" "$path" "$got" "$want"
    [ -s /tmp/resp.json ] && sed 's/^/       /' /tmp/resp.json
  fi
}

check() { # check <name> <condition-result(0/1)>  — extra assertions on bodies
  if [ "$2" = "0" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-42s\n' "$1"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-42s (%s)\n' "$1" "${3:-}"; fi
}

echo "== health & auth =="
t - "healthz" 200 GET /healthz
t - "login wrong password" 401 POST /api/v1/auth/login "{\"username\":\"$ADMIN_USER\",\"password\":\"wrong-$SFX\"}"
t "$AJ" "login admin" 200 POST /api/v1/auth/login "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}"
check "admin is_admin" "$(jq -e '.is_admin == true' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "me" 200 GET /api/v1/auth/me
t - "fs without cookie" 401 GET /api/v1/fs?path=/

echo "== admin fs CRUD (bypass) =="
D="/docs-$SFX"
t "$AJ" "mkdir $D" 201 POST /api/v1/fs/mkdir "{\"path\":\"$D\"}"
t "$AJ" "mkdir duplicate" 409 POST /api/v1/fs/mkdir "{\"path\":\"$D\"}"
t "$AJ" "write note" 200 POST /api/v1/fs/write "{\"path\":\"$D/note.txt\",\"content\":\"hello acl $SFX\"}"
t "$AJ" "read note" 200 GET "/api/v1/fs/content?path=$D/note.txt"
C=$(jq -r '.content' /tmp/resp.json)
check "content round-trip" "$([ "$C" = "hello acl $SFX" ]; echo $?)" "got: $C"
t "$AJ" "list dir" 200 GET "/api/v1/fs?path=$D"
N=$(jq -r '.entries | length' /tmp/resp.json)
check "list has 1 entry" "$([ "$N" = 1 ]; echo $?)" "got $N"
t "$AJ" "rename" 200 POST /api/v1/fs/rename "{\"from\":\"$D/note.txt\",\"to\":\"$D/renamed.txt\"}"
t "$AJ" "rename onto existing" 409 POST /api/v1/fs/rename "{\"from\":\"$D/renamed.txt\",\"to\":\"$D/renamed.txt\"}"
t "$AJ" "delete file" 204 DELETE "/api/v1/fs?path=$D/renamed.txt"
printf 'up-%s\n' "$SFX" > "/tmp/up-$SFX.txt"
UP=$(curl -s -o /tmp/resp.json -w '%{http_code}' -b "$AJ" -F "path=$D" -F "file=@/tmp/up-$SFX.txt" "$BASE_URL/api/v1/fs/upload")
check "upload 201" "$([ "$UP" = 201 ]; echo $?)" "got $UP"
RAW=$(curl -s -b "$AJ" "$BASE_URL/api/v1/fs/raw?path=$D/up-$SFX.txt")
check "raw download matches" "$([ "$RAW" = "up-$SFX" ]; echo $?)" "got: $RAW"
t "$AJ" "delete non-empty dir" 409 DELETE "/api/v1/fs?path=$D"
t "$AJ" "traversal blocked" 400 GET "/api/v1/fs?path=$D/../etc"

echo "== users, groups, grants =="
t "$AJ" "create user $USER" 201 POST /api/v1/users "{\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"
t "$AJ" "create group $GROUP" 201 POST /api/v1/groups "{\"name\":\"$GROUP\"}"
GID=$(jq -r '.id' /tmp/resp.json)
t "$AJ" "add member" 204 POST "/api/v1/groups/$GID/members" "{\"username\":\"$USER\"}"
t "$AJ" "grant r on $D to group" 200 PUT /api/v1/perms "{\"path\":\"$D\",\"principal_type\":\"group\",\"principal\":\"$GROUP\",\"read\":true}"
t "$UJ" "login $USER" 200 POST /api/v1/auth/login "{\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"

echo "== enforcement for $USER (group read-only) =="
t "$UJ" "list $D" 200 GET "/api/v1/fs?path=$D"
t "$UJ" "read file" 200 GET "/api/v1/fs/content?path=$D/up-$SFX.txt"
t "$UJ" "write denied" 403 POST /api/v1/fs/write "{\"path\":\"$D/evil.txt\",\"content\":\"no\"}"
t "$UJ" "delete denied" 403 DELETE "/api/v1/fs?path=$D/up-$SFX.txt"
t "$UJ" "mkdir denied" 403 POST /api/v1/fs/mkdir "{\"path\":\"$D/sub\"}"
t "$UJ" "admin API denied" 403 GET /api/v1/users
t "$UJ" "perms PUT denied" 403 PUT /api/v1/perms "{\"path\":\"/\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"read\":true}"
t "$UJ" "grant outside tree denied (no grants)" 403 GET "/api/v1/fs?path=/docs-999999-$SFX"

echo "== user-direct grant overrides group =="
t "$AJ" "grant rw to $USER on $D/incoming" 200 PUT /api/v1/perms "{\"path\":\"$D/incoming\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"read\":true,\"write\":true}"
t "$UJ" "write under direct grant" 200 POST /api/v1/fs/write "{\"path\":\"$D/incoming/ok.txt\",\"content\":\"by $USER\"}"
t "$UJ" "effective shows source" 200 GET "/api/v1/perms?path=$D/incoming"
S=$(jq -r '.effective.source' /tmp/resp.json)
check "effective.source == $D/incoming" "$([ "$S" = "$D/incoming" ]; echo $?)" "got: $S"
t "$UJ" "delete still denied (no d bit)" 403 DELETE "/api/v1/fs?path=$D/incoming/ok.txt"

echo "== visibility filtering =="
t "$AJ" "grant r on / to group" 200 PUT /api/v1/perms "{\"path\":\"/\",\"principal_type\":\"group\",\"principal\":\"$GROUP\",\"read\":true}"
t "$AJ" "mkdir /hide-$SFX (no grants)" 201 POST /api/v1/fs/mkdir "{\"path\":\"/hide-$SFX\"}"
t "$AJ" "zero-grant on /hide-$SFX excludes via inheritance" 200 PUT /api/v1/perms "{\"path\":\"/hide-$SFX\",\"principal_type\":\"group\",\"principal\":\"$GROUP\",\"read\":false}"
t "$UJ" "list / allowed now" 200 GET "/api/v1/fs?path=/"
HAS=$(jq -e --arg d "$D" 'any(.entries[].entry.path; . == $d)' /tmp/resp.json >/dev/null; echo $?)
check "root list contains $D" "$HAS"
HID=$(jq -e --arg h "/hide-$SFX" 'any(.entries[].entry.path; . == $h)' /tmp/resp.json >/dev/null; echo $?)
check "root list hides /hide-$SFX" "$([ "$HID" != 0 ]; echo $?)"

echo "== session lifecycle =="
t "$UJ" "logout" 204 POST /api/v1/auth/logout
t "$UJ" "fs after logout" 401 GET /api/v1/fs?path=/

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
