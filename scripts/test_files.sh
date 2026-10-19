#!/usr/bin/env bash
# Regression tests for the multi-tenant file-explorer API (auth + ACLs + fs ops).
# Usage: BASE_URL=http://localhost:8095 ADMIN_TENANT=system ADMIN_PASS=secret ./scripts/test_files.sh
# Assumes the bootstrap SUPER admin exists; creates a throwaway tenant, users,
# groups and paths with a random suffix; safe to re-run.
set -u
BASE_URL="${BASE_URL:-http://localhost:8080}"
ADMIN_TENANT="${ADMIN_TENANT:-system}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-admin}"
SFX=$RANDOM
AJ=/tmp/fx-admin-$$.jar   # super admin cookie jar
UJ=/tmp/fx-user-$$.jar    # regular user cookie jar
TAJ=/tmp/fx-tadmin-$$.jar # tenant admin cookie jar
BJ=/tmp/fx-brief-$$.jar # short-lived customer cookie jar
T="t$SFX"                 # throwaway tenant
USER="u$SFX"
GROUP="g$SFX"
PASS=0 FAIL=0

cleanup() { rm -f "$AJ" "$UJ" "$TAJ" "$BJ"; }
trap cleanup EXIT

t() { # t <jar|-> <name> <want_status> <METHOD> <path> [json-body]
  local jar=$1 name=$2 want=$3 method=$4 path=$5 body=${6:-}
  local args=(-s -o /tmp/resp.json -w '%{http_code}' -X "$method" "$BASE_URL$path")
  [ "$jar" != "-" ] && args+=(-b "$jar" -c "$jar")
  [ -n "$body" ] && args+=(-H 'Content-Type: application/json' -d "$body")
  local got; got=$(curl "${args[@]}")
  if [ "$got" = "$want" ]; then
    PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-46s %s %s -> %s\n' "$name" "$method" "$path" "$got"
  else
    FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-46s %s %s -> got %s want %s\n' "$name" "$method" "$path" "$got" "$want"
    [ -s /tmp/resp.json ] && sed 's/^/       /' /tmp/resp.json
  fi
}

check() { # check <name> <condition-result(0/1)> [detail]
  if [ "$2" = "0" ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-46s\n' "$1"
  else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-46s (%s)\n' "$1" "${3:-}"; fi
}

tb() { # tb <bearer-key> <name> <want_status> <METHOD> <path> — like t() but with an Authorization header
  local key=$1 name=$2 want=$3 method=$4 path=$5
  local got; got=$(curl -s -o /tmp/resp.json -w '%{http_code}' -X "$method" -H "Authorization: Bearer $key" "$BASE_URL$path")
  if [ "$got" = "$want" ]; then
    PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-46s %s %s -> %s\n' "$name" "$method" "$path" "$got"
  else
    FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-46s %s %s -> got %s want %s\n' "$name" "$method" "$path" "$got" "$want"
    [ -s /tmp/resp.json ] && sed 's/^/       /' /tmp/resp.json
  fi
}

echo "== health & auth =="
t - "healthz" 200 GET /healthz
t - "login no tenant" 422 POST /api/v1/auth/login "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}"
t - "login wrong tenant" 401 POST /api/v1/auth/login "{\"tenant\":\"nope-$SFX\",\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}"
t - "login wrong password" 401 POST /api/v1/auth/login "{\"tenant\":\"$ADMIN_TENANT\",\"username\":\"$ADMIN_USER\",\"password\":\"wrong-$SFX\"}"
t "$AJ" "login super admin" 200 POST /api/v1/auth/login "{\"tenant\":\"$ADMIN_TENANT\",\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}"
check "admin is_super" "$(jq -e '.is_super == true' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "me" 200 GET /api/v1/auth/me
t - "fs without cookie" 401 GET /api/v1/fs?path=/

echo "== tenants (super only) =="
t "$AJ" "create tenant $T" 201 POST /api/v1/tenants "{\"name\":\"$T\"}"
t "$AJ" "duplicate tenant" 409 POST /api/v1/tenants "{\"name\":\"$T\"}"

echo "== super admin fs CRUD (bypass) =="
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
ET=$(curl -s -D- -o /dev/null -b "$AJ" "$BASE_URL/api/v1/fs/raw?path=$D/up-$SFX.txt" | tr -d '\r' | grep -i '^etag:' | awk '{print $2}')
GOT=$(curl -s -o /dev/null -w '%{http_code}' -b "$AJ" -H "If-None-Match: $ET" "$BASE_URL/api/v1/fs/raw?path=$D/up-$SFX.txt")
check "conditional GET: ETag -> 304" "$([ "$GOT" = 304 ]; echo $?)" "got: $GOT etag: $ET"
t "$AJ" "delete non-empty dir" 409 DELETE "/api/v1/fs?path=$D"
t "$AJ" "traversal blocked" 400 GET "/api/v1/fs?path=$D/../etc"

echo "== users, groups, grants in tenant $T =="
t "$AJ" "create user $T/$USER" 201 POST /api/v1/users "{\"tenant\":\"$T\",\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"
t "$AJ" "create group $T/$GROUP" 201 POST /api/v1/groups "{\"tenant\":\"$T\",\"name\":\"$GROUP\"}"
GID=$(jq -r '.id' /tmp/resp.json)
t "$AJ" "add member" 204 POST "/api/v1/groups/$GID/members" "{\"username\":\"$USER\"}"
t "$AJ" "member name resolves in group tenant" 404 POST "/api/v1/groups/$GID/members" "{\"username\":\"$ADMIN_USER\"}"
t "$AJ" "grant r on $D to group" 200 PUT /api/v1/perms "{\"path\":\"$D\",\"principal_type\":\"group\",\"principal\":\"$GROUP\",\"tenant\":\"$T\",\"read\":true}"
t "$UJ" "login $USER" 200 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"
t - "same username other tenant denied" 401 POST /api/v1/auth/login "{\"tenant\":\"$ADMIN_TENANT\",\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"

echo "== enforcement for $T/$USER (group read-only) =="
t "$UJ" "list $D" 200 GET "/api/v1/fs?path=$D"
t "$UJ" "read file" 200 GET "/api/v1/fs/content?path=$D/up-$SFX.txt"
t "$UJ" "write denied" 403 POST /api/v1/fs/write "{\"path\":\"$D/evil.txt\",\"content\":\"no\"}"
t "$UJ" "delete denied" 403 DELETE "/api/v1/fs?path=$D/up-$SFX.txt"
t "$UJ" "mkdir denied" 403 POST /api/v1/fs/mkdir "{\"path\":\"$D/sub\"}"
t "$UJ" "admin API denied" 403 GET /api/v1/users
t "$UJ" "perms PUT denied" 403 PUT /api/v1/perms "{\"path\":\"/\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"read\":true}"
t "$UJ" "grant outside tree denied (no grants)" 403 GET "/api/v1/fs?path=/docs-999999-$SFX"

echo "== user-direct grant overrides group =="
t "$AJ" "grant rw to $USER on $D/incoming" 200 PUT /api/v1/perms "{\"path\":\"$D/incoming\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"tenant\":\"$T\",\"read\":true,\"write\":true}"
t "$UJ" "write under direct grant" 200 POST /api/v1/fs/write "{\"path\":\"$D/incoming/ok.txt\",\"content\":\"by $USER\"}"
t "$UJ" "effective shows source" 200 GET "/api/v1/perms?path=$D/incoming"
S=$(jq -r '.effective.source' /tmp/resp.json)
check "effective.source == $D/incoming" "$([ "$S" = "$D/incoming" ]; echo $?)" "got: $S"
t "$UJ" "delete still denied (no d bit)" 403 DELETE "/api/v1/fs?path=$D/incoming/ok.txt"

echo "== expiring access (customer shares) =="
now2() { date -u -v+2S +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -d '+2 seconds' +%Y-%m-%dT%H:%M:%SZ; }
SHARE="/share-$SFX"
t "$AJ" "mkdir $SHARE" 201 POST /api/v1/fs/mkdir "{\"path\":\"$SHARE\"}"
t "$AJ" "write deliverable" 200 POST /api/v1/fs/write "{\"path\":\"$SHARE/deliv.txt\",\"content\":\"3-day dropbox\"}"
t "$AJ" "grant with past expiry" 200 PUT /api/v1/perms "{\"path\":\"$SHARE\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"tenant\":\"$T\",\"read\":true,\"expires_at\":\"2000-01-01T00:00:00Z\"}"
t "$UJ" "past-expiry grant invisible" 403 GET "/api/v1/fs?path=$SHARE"
t "$UJ" "perms view flags expired" 200 GET "/api/v1/perms?path=$SHARE"
EX=$(jq -e 'any(.grants[] | select(.principal_type=="user" and .principal_name=="'"$USER"'"); .expired)' /tmp/resp.json >/dev/null; echo $?)
check "grant row carries expired=true" "$EX"
t "$AJ" "negative days rejected" 422 PUT /api/v1/perms "{\"path\":\"$SHARE\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"tenant\":\"$T\",\"read\":true,\"expires_days\":-1}"
t "$AJ" "grant 2s window" 200 PUT /api/v1/perms "{\"path\":\"$SHARE\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"tenant\":\"$T\",\"read\":true,\"expires_at\":\"$(now2)\"}"
t "$UJ" "share readable in window" 200 GET "/api/v1/fs?path=$SHARE"
sleep 3
t "$UJ" "share gone after window" 403 GET "/api/v1/fs?path=$SHARE"
t "$AJ" "renew via expires_days=3" 200 PUT /api/v1/perms "{\"path\":\"$SHARE\",\"principal_type\":\"user\",\"principal\":\"$USER\",\"tenant\":\"$T\",\"read\":true,\"expires_days\":3}"
HAS=$(jq -e '.expires_at != null and .expired == false' /tmp/resp.json >/dev/null; echo $?)
check "expires_days=3 stored as future expires_at" "$HAS"
t "$UJ" "renewed share readable again" 200 GET "/api/v1/fs?path=$SHARE"
t "$AJ" "create lapsed account" 201 POST /api/v1/users "{\"tenant\":\"$T\",\"username\":\"gone$SFX\",\"password\":\"pw\",\"expires_at\":\"2000-01-01T00:00:00Z\"}"
t - "lapsed account login rejected" 401 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"gone$SFX\",\"password\":\"pw\"}"
t "$AJ" "create 2s-lived account" 201 POST /api/v1/users "{\"tenant\":\"$T\",\"username\":\"brief$SFX\",\"password\":\"pw\",\"expires_at\":\"$(now2)\"}"
t "$AJ" "grant for brief account" 200 PUT /api/v1/perms "{\"path\":\"$SHARE\",\"principal_type\":\"user\",\"principal\":\"brief$SFX\",\"tenant\":\"$T\",\"read\":true}"
t "$BJ" "login brief account" 200 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"brief$SFX\",\"password\":\"pw\"}"
t "$BJ" "brief account reads share" 200 GET "/api/v1/fs?path=$SHARE"
sleep 3
t "$BJ" "session dead after account lapse" 401 GET "/api/v1/fs?path=$SHARE"
t - "brief cannot relogin" 401 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"brief$SFX\",\"password\":\"pw\"}"

echo "== device API keys (non-interactive/IoT) =="
DEV="dev$SFX"
t "$AJ" "device user without password" 201 POST /api/v1/users "{\"tenant\":\"$T\",\"username\":\"$DEV\"}"
DEVID=$(jq -r '.id' /tmp/resp.json)
t - "device cannot login interactively" 401 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"$DEV\",\"password\":\"guess\"}"
t "$AJ" "ed25519 reserved, rejected" 422 POST /api/v1/users/$DEVID/factors "{\"method\":\"ed25519\"}"
t "$AJ" "oauth2 reserved, rejected" 422 POST /api/v1/users/$DEVID/factors "{\"method\":\"oauth2\",\"alg\":\"google\"}"
t "$AJ" "provision apikey" 201 POST /api/v1/users/$DEVID/factors "{\"method\":\"apikey\",\"label\":\"v1\"}"
KEY=$(jq -r '.key' /tmp/resp.json)
FID=$(jq -r '.factor.id' /tmp/resp.json)
check "key looks high-entropy (43 chars)" "$([ ${#KEY} -eq 43 ] && echo 0 || echo 1)"
t "$AJ" "duplicate label rejected" 409 POST /api/v1/users/$DEVID/factors "{\"method\":\"apikey\",\"label\":\"v1\"}"
t "$AJ" "grant r on $SHARE to device" 200 PUT /api/v1/perms "{\"path\":\"$SHARE\",\"principal_type\":\"user\",\"principal\":\"$DEV\",\"tenant\":\"$T\",\"read\":true}"
tb "$KEY" "bearer request works" 200 GET "/api/v1/fs?path=$SHARE"
tb "bogus000000000000000000000000000000000000000000" "bogus key rejected" 401 GET "/api/v1/fs?path=$SHARE"
tb "$KEY" "ACL applies to bearer too" 403 GET "/api/v1/fs?path=/nope-$SFX"
t "$AJ" "factor listed (no secrets)" 200 GET /api/v1/users/$DEVID/factors
check "secret never listed, last_used recorded" "$(jq -e 'all(.[]; has("secret") | not) and any(.[]; .last_used_at != null)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "lapsed key refused at creation" 201 POST /api/v1/users/$DEVID/factors "{\"method\":\"apikey\",\"label\":\"dead\",\"expires_at\":\"2000-01-01T00:00:00Z\"}"
DEADKEY=$(jq -r '.key' /tmp/resp.json)
tb "$DEADKEY" "lapsed key gets 401" 401 GET "/api/v1/fs?path=$SHARE"
t "$AJ" "revoke apikey" 204 DELETE /api/v1/users/$DEVID/factors/$FID
tb "$KEY" "revocation bites next request" 401 GET "/api/v1/fs?path=$SHARE"

echo "== visibility filtering =="
t "$AJ" "grant r on / to group" 200 PUT /api/v1/perms "{\"path\":\"/\",\"principal_type\":\"group\",\"principal\":\"$GROUP\",\"tenant\":\"$T\",\"read\":true}"
t "$AJ" "mkdir /hide-$SFX (no grants)" 201 POST /api/v1/fs/mkdir "{\"path\":\"/hide-$SFX\"}"
t "$AJ" "zero-grant on /hide-$SFX excludes via inheritance" 200 PUT /api/v1/perms "{\"path\":\"/hide-$SFX\",\"principal_type\":\"group\",\"principal\":\"$GROUP\",\"tenant\":\"$T\",\"read\":false}"
t "$UJ" "list / allowed now" 200 GET "/api/v1/fs?path=/"
HAS=$(jq -e --arg d "$D" 'any(.entries[].entry.path; . == $d)' /tmp/resp.json >/dev/null; echo $?)
check "root list contains $D" "$HAS"
HID=$(jq -e --arg h "/hide-$SFX" 'any(.entries[].entry.path; . == $h)' /tmp/resp.json >/dev/null; echo $?)
check "root list hides /hide-$SFX" "$([ "$HID" != 0 ]; echo $?)"

echo "== tenant admin scope =="
t "$AJ" "create tenant admin ta$SFX" 201 POST /api/v1/users "{\"tenant\":\"$T\",\"username\":\"ta$SFX\",\"password\":\"pw-$SFX\",\"is_admin\":true}"
t "$TAJ" "login ta$SFX" 200 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"ta$SFX\",\"password\":\"pw-$SFX\"}"
check "ta is_admin not super" "$(jq -e '.is_admin == true and .is_super == false' /tmp/resp.json >/dev/null; echo $?)"
t "$TAJ" "list users (own tenant)" 200 GET /api/v1/users
ONLY=$(jq -e --arg t "$T" 'all(.[]; .tenant == $t)' /tmp/resp.json >/dev/null; echo $?)
check "user list scoped to $T" "$ONLY"
SEESYS=$(jq -e --arg u "$ADMIN_USER" 'any(.[]; .username == $u)' /tmp/resp.json >/dev/null; echo $?)
check "user list hides $ADMIN_TENANT/$ADMIN_USER" "$([ "$SEESYS" != 0 ]; echo $?)"
t "$TAJ" "create user pinned to own tenant" 201 POST /api/v1/users "{\"tenant\":\"$ADMIN_TENANT\",\"username\":\"pin$SFX\",\"password\":\"pw-$SFX\"}"
PIN=$(jq -r '.tenant' /tmp/resp.json)
check "created user tenant == $T" "$([ "$PIN" = "$T" ]; echo $?)" "got: $PIN"
t "$TAJ" "grant to foreign tenant denied" 403 PUT /api/v1/perms "{\"path\":\"/\",\"principal_type\":\"user\",\"principal\":\"$ADMIN_USER\",\"tenant\":\"$ADMIN_TENANT\",\"read\":true}"
t "$TAJ" "tenant admin has no file bypass" 403 GET "/api/v1/fs?path=/hide-$SFX"
t "$TAJ" "tenants CRUD denied" 403 GET /api/v1/tenants
t "$TAJ" "tenant create denied" 403 POST /api/v1/tenants "{\"name\":\"x-$SFX\"}"
SYSID=$(curl -sb "$AJ" "$BASE_URL/api/v1/users" | jq -r --arg t "$ADMIN_TENANT" --arg u "$ADMIN_USER" '.[] | select(.tenant==$t and .username==$u) | .id')
t "$TAJ" "delete foreign user denied" 403 DELETE "/api/v1/users/$SYSID"
t "$TAJ" "own grant then read works" 200 PUT /api/v1/perms "{\"path\":\"/hide-$SFX\",\"principal_type\":\"user\",\"principal\":\"ta$SFX\",\"read\":true}"
t "$TAJ" "file access via own grant" 200 GET "/api/v1/fs?path=/hide-$SFX"

echo "== tenant delete cascades =="
TMP="tmp-$SFX"
t "$AJ" "create tenant $TMP" 201 POST /api/v1/tenants "{\"name\":\"$TMP\"}"
TID=$(jq -r '.id' /tmp/resp.json)
t "$AJ" "user in $TMP" 201 POST /api/v1/users "{\"tenant\":\"$TMP\",\"username\":\"doomed$SFX\",\"password\":\"pw\"}"
t "$TAJ" "grant $TMP/u on $D" 403 PUT /api/v1/perms "{\"path\":\"$D\",\"principal_type\":\"user\",\"principal\":\"doomed$SFX\",\"tenant\":\"$TMP\",\"read\":true}"
t "$AJ" "grant $TMP/u on $D" 200 PUT /api/v1/perms "{\"path\":\"$D\",\"principal_type\":\"user\",\"principal\":\"doomed$SFX\",\"tenant\":\"$TMP\",\"read\":true}"
t "$AJ" "delete tenant $TMP" 204 DELETE "/api/v1/tenants/$TID"
t "$AJ" "perms on $D after cascade" 200 GET "/api/v1/perms?path=$D"
GC=$(jq -r '[.grants[] | select(.principal_tenant=="'$TMP'")] | length' /tmp/resp.json)
check "grants for $TMP removed" "$([ "$GC" = 0 ]; echo $?)" "got $GC"
t "$UJ" "list $D still fine" 200 GET "/api/v1/fs?path=$D"

echo "== suspend / reinstate (active kill switch) =="
USEQ=$(curl -sb "$AJ" "$BASE_URL/api/v1/users" | jq -r --arg t "$T" --arg u "$USER" '.[] | select(.tenant==$t and .username==$u) | .id')
t "$AJ" "suspend $USER" 200 POST "/api/v1/users/$USEQ/active" '{"active":false}'
t "$UJ" "suspended: live session dies" 401 GET /api/v1/auth/me
t "$UJ" "suspended: fs refused too" 401 GET "/api/v1/fs?path=$D"
t - "suspended: login refused (like wrong pw)" 401 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"
t "$AJ" "grant survives suspension" 200 GET "/api/v1/perms?path=$D/incoming"
INERT=$(jq -e --arg u "$USER" 'all(.grants[] | select(.principal_type=="user" and .principal_name==$u); .principal_active == false)' /tmp/resp.json >/dev/null; echo $?)
check "grant flags inactive principal" "$INERT"
MEID=$(curl -sb "$AJ" -X POST "$BASE_URL/api/v1/auth/login" -H 'Content-Type: application/json' -d "{\"tenant\":\"$ADMIN_TENANT\",\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}" | jq -r '.id')
t "$AJ" "cannot suspend own account" 422 POST "/api/v1/users/$MEID/active" '{"active":false}'
t "$TAJ" "suspend foreign tenant denied" 403 POST "/api/v1/users/$MEID/active" '{"active":false}'
t "$AJ" "reinstate $USER" 200 POST "/api/v1/users/$USEQ/active" '{"active":true}'
t "$UJ" "re-login after reinstate" 200 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"$USER\",\"password\":\"pw-$SFX\"}"
t "$UJ" "access restored" 200 GET "/api/v1/fs?path=$D"
t "$AJ" "provision device key v2" 201 POST "/api/v1/users/$DEVID/factors" '{"method":"apikey","label":"v2"}'
V2KEY=$(jq -r '.key' /tmp/resp.json)
tb "$V2KEY" "device works before suspend" 200 GET "/api/v1/fs?path=$SHARE"
t "$AJ" "suspend device" 200 POST "/api/v1/users/$DEVID/active" '{"active":false}'
tb "$V2KEY" "device key dies with account" 401 GET "/api/v1/fs?path=$SHARE"
t "$AJ" "reinstate device" 200 POST "/api/v1/users/$DEVID/active" '{"active":true}'
tb "$V2KEY" "device lives again" 200 GET "/api/v1/fs?path=$SHARE"
t "$AJ" "suspend group $GROUP" 200 POST "/api/v1/groups/$GID/active" '{"active":false}'
t "$UJ" "group grant inert while suspended" 403 GET "/api/v1/fs?path=$D"
t "$UJ" "direct grant untouched by group suspend" 200 POST /api/v1/fs/write "{\"path\":\"$D/incoming/alive-$SFX.txt\",\"content\":\"x\"}"
t "$AJ" "reinstate group" 200 POST "/api/v1/groups/$GID/active" '{"active":true}'
t "$UJ" "group grant back" 200 GET "/api/v1/fs?path=$D"
TAID=$(curl -sb "$AJ" "$BASE_URL/api/v1/users" | jq -r --arg t "$T" --arg u "ta$SFX" '.[] | select(.tenant==$t and .username==$u) | .id')
t "$AJ" "suspend tenant admin" 200 POST "/api/v1/users/$TAID/active" '{"active":false}'
t "$TAJ" "suspended admin: admin API dead" 401 GET /api/v1/users
t "$AJ" "reinstate tenant admin" 200 POST "/api/v1/users/$TAID/active" '{"active":true}'
t "$TAJ" "re-login after reinstate" 200 POST /api/v1/auth/login "{\"tenant\":\"$T\",\"username\":\"ta$SFX\",\"password\":\"pw-$SFX\"}"

echo "== draft/publish working copies (config editor) =="
t "$AJ" "mkdir cfg dir" 201 POST /api/v1/fs/mkdir "{\"path\":\"$D/cfg\"}"
t "$AJ" "write app.yml" 200 POST /api/v1/fs/write "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: 1\n\"}"
t "$AJ" "draft none" 200 GET "/api/v1/fs/draft?path=$D/cfg/app.yml"
check "draft null before any save" "$(jq -e '.draft==null' /tmp/resp.json >/dev/null; echo $?)"
t "$UJ" "draft view allowed with r" 200 GET "/api/v1/fs/draft?path=$D/cfg/app.yml"
t "$UJ" "draft save denied without w" 403 PUT /api/v1/fs/draft "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: 2\n\"}"
t "$UJ" "draft commit denied without w" 403 POST /api/v1/fs/draft/commit "{\"path\":\"$D/cfg/app.yml\"}"
t "$UJ" "draft discard denied without w" 403 DELETE "/api/v1/fs/draft?path=$D/cfg/app.yml"
t "$AJ" "draft save" 200 PUT /api/v1/fs/draft "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: 2\n\"}"
t "$AJ" "draft save again" 200 PUT /api/v1/fs/draft "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: 3\n\"}"
t "$AJ" "draft read" 200 GET "/api/v1/fs/draft?path=$D/cfg/app.yml"
check "draft holds latest save (last writer wins)" "$(jq -e '.draft.content=="a: 3\n"' /tmp/resp.json >/dev/null; echo $?)"
check "draft names the last writer" "$(jq -e --arg b "$ADMIN_TENANT/$ADMIN_USER" '.draft.updated_by==$b' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "save invalid yaml draft" 200 PUT /api/v1/fs/draft "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: [1,\n\"}"
t "$AJ" "commit invalid yaml refused" 422 POST /api/v1/fs/draft/commit "{\"path\":\"$D/cfg/app.yml\"}"
t "$AJ" "file read after refused commit" 200 GET "/api/v1/fs/content?path=$D/cfg/app.yml"
check "refused commit did not touch the file" "$(jq -e '.content=="a: 1\n"' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "draft read after refused commit" 200 GET "/api/v1/fs/draft?path=$D/cfg/app.yml"
check "refused commit kept the draft" "$(jq -e '.draft.content=="a: [1,\n"' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "save valid multi-doc draft" 200 PUT /api/v1/fs/draft "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: 2 # keep\n---\nb: [1, 2]\n\"}"
t "$AJ" "commit draft" 200 POST /api/v1/fs/draft/commit "{\"path\":\"$D/cfg/app.yml\"}"
t "$AJ" "file read after commit" 200 GET "/api/v1/fs/content?path=$D/cfg/app.yml"
check "committed bytes landed" "$(jq -e '.content=="a: 2 # keep\n---\nb: [1, 2]\n"' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "draft gone after commit" 200 GET "/api/v1/fs/draft?path=$D/cfg/app.yml"
check "commit dropped the draft" "$(jq -e '.draft==null' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "commit without draft conflicts" 409 POST /api/v1/fs/draft/commit "{\"path\":\"$D/cfg/app.yml\"}"
t "$AJ" "save again for discard" 200 PUT /api/v1/fs/draft "{\"path\":\"$D/cfg/app.yml\",\"content\":\"a: 9\n\"}"
t "$AJ" "discard draft" 204 DELETE "/api/v1/fs/draft?path=$D/cfg/app.yml"
t "$AJ" "discard is idempotent" 204 DELETE "/api/v1/fs/draft?path=$D/cfg/app.yml"

echo "== base_mtime optimistic guard on /fs/write =="
t "$AJ" "content exposes mtime" 200 GET "/api/v1/fs/content?path=$D/cfg/app.yml"
MT=$(jq -r '.mtime // 0' /tmp/resp.json)
check "mtime is a plausible unix ts" "$([ "${MT:-0}" -gt 1500000000 ]; echo $?)" "mtime=$MT"
t "$AJ" "write with matching base_mtime" 200 POST /api/v1/fs/write "{\"path\":\"$D/cfg/app.yml\",\"content\":\"guarded\",\"base_mtime\":$MT}"
t "$AJ" "write with stale base_mtime" 409 POST /api/v1/fs/write "{\"path\":\"$D/cfg/app.yml\",\"content\":\"clobber\",\"base_mtime\":$((MT-1))}"
t "$AJ" "read after conflict" 200 GET "/api/v1/fs/content?path=$D/cfg/app.yml"
check "stale write did not clobber" "$(jq -e '.content=="guarded"' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "base_mtime on missing file conflicts" 409 POST /api/v1/fs/write "{\"path\":\"$D/cfg/gone.yml\",\"content\":\"x\",\"base_mtime\":1}"
t "$AJ" "explicit null base_mtime behaves as before" 200 POST /api/v1/fs/write "{\"path\":\"$D/cfg/plain.txt\",\"content\":\"old contract\",\"base_mtime\":null}"

echo "== forensic event trail =="
t "$AJ" "grant list for revoke" 200 GET "/api/v1/perms?path=/hide-$SFX"
GPID=$(jq -r '.grants[0].id' /tmp/resp.json)
t "$AJ" "revoke grant (DML to record)" 204 DELETE "/api/v1/perms/$GPID"
t "$AJ" "events listed" 200 GET "/api/v1/events?limit=500"
for a in tenant_create tenant_delete user_create group_create member_add perm_set perm_delete factor_create factor_delete user_active login_failed fs_commit; do
  CNT=$(jq --arg a "$a" 'map(select(.action==$a)) | length' /tmp/resp.json)
  check "event $a recorded" "$([ "${CNT:-0}" -gt 0 ]; echo $?)" "cnt=$CNT"
done
WHA=$(jq -e 'all(.[]; (.actor // "") != "" and (.ip // "") != "")' /tmp/resp.json >/dev/null; echo $?)
check "every event names actor + source ip" "$WHA"
ORDER=$(jq -e '.[0].ts >= .[-1].ts' /tmp/resp.json >/dev/null; echo $?)
check "events newest-first" "$ORDER"
check "plaintext keys never in audit" "$(! grep -qF -- "$V2KEY" /tmp/resp.json && ! grep -qF -- "$KEY" /tmp/resp.json; echo $?)"
t "$AJ" "filter by action" 200 GET "/api/v1/events?action=user_active"
FILT=$(jq -e 'all(.[]; .action == "user_active")' /tmp/resp.json >/dev/null; echo $?)
check "action filter respected" "$FILT"
t "$AJ" "bad limit rejected" 400 GET "/api/v1/events?limit=abc"
t "$AJ" "bad since rejected" 400 GET "/api/v1/events?since=nope"
t "$UJ" "events are admin-only" 403 GET /api/v1/events
TTID=$(curl -sb "$AJ" "$BASE_URL/api/v1/tenants" | jq -r --arg t "$T" '.[] | select(.name==$t) | .id')
t "$TAJ" "events as tenant admin" 200 GET "/api/v1/events?limit=500"
SCOPED=$(jq -e --argjson t "$TTID" 'all(.[]; .tenant_id == $t)' /tmp/resp.json >/dev/null; echo $?)
check "ta events scoped to $T only" "$SCOPED"
SEESUPER=$(jq -e 'any(.[]; .action == "user_create" and (.actor | startswith("'$ADMIN_TENANT'/")))' /tmp/resp.json >/dev/null; echo $?)
check "ta sees super admin actions on own tenant" "$SEESUPER"
RIDHDR=$(curl -s -D - -o /dev/null "$BASE_URL/healthz" | grep -ci '^x-request-id: [0-9a-f]\{16\}' || true)
check "X-Request-Id issued on every response" "$([ "$RIDHDR" -ge 1 ]; echo $?)"

echo "== pagination, search, sort =="
tot() { # tot <name> <want_total> <path> — GET, then verify 200 + X-Total-Count header
  local name=$1 want=$2 path=$3 code got
  code=$(curl -s -o /tmp/resp.json -D /tmp/hdr.txt -b "$AJ" -w '%{http_code}' "$BASE_URL$path")
  got=$(tr -d '\r' < /tmp/hdr.txt | awk -F': ' 'tolower($1)=="x-total-count" {print $2}')
  if [ "$code" = 200 ] && [ "$got" = "$want" ]; then
    PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-46s X-Total-Count %s\n' "$name" "$got"
  else
    FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-46s (status %s, total %s, want %s)\n' "$name" "$code" "$got" "$want"
  fi
}
T2="pg$SFX"
t "$AJ" "create tenant $T2" 201 POST /api/v1/tenants "{\"name\":\"$T2\"}"
for n in 1 2 3; do
  t "$AJ" "create pgu$n" 201 POST /api/v1/users "{\"tenant\":\"$T2\",\"username\":\"pgu$n\",\"password\":\"pw\"}"
done
PD="/pgdir-$SFX"
t "$AJ" "mkdir $PD" 201 POST /api/v1/fs/mkdir "{\"path\":\"$PD\"}"
t "$AJ" "mkdir $PD/sub1" 201 POST /api/v1/fs/mkdir "{\"path\":\"$PD/sub1\"}"
t "$AJ" "write pa.txt" 200 POST /api/v1/fs/write "{\"path\":\"$PD/pa.txt\",\"content\":\"aa\"}"
t "$AJ" "write pb.txt" 200 POST /api/v1/fs/write "{\"path\":\"$PD/pb.txt\",\"content\":\"aaaa\"}"
t "$AJ" "write pc.txt" 200 POST /api/v1/fs/write "{\"path\":\"$PD/pc.txt\",\"content\":\"aaaaaa\"}"

# paging: total stays constant, pages are disjoint and ordered
tot "users total by tenant q" 3 "/api/v1/users?q=$T2"
t "$AJ" "users limit=2" 200 GET "/api/v1/users?q=$T2&limit=2"
N=$(jq 'length' /tmp/resp.json)
check "users page1 has 2 rows" "$([ "$N" = 2 ]; echo $?)" "got $N"
tot "users total under paging" 3 "/api/v1/users?q=$T2&limit=2&offset=0"
P1=$(curl -s -b "$AJ" "$BASE_URL/api/v1/users?q=$T2&limit=2&offset=0" | jq -c '[.[].username]')
P2=$(curl -s -b "$AJ" "$BASE_URL/api/v1/users?q=$T2&limit=2&offset=2" | jq -c '[.[].username]')
OV=$(jq -n --argjson a "$P1" --argjson b "$P2" '[$a[] | . as $x | select($b | index($x))] | length')
check "users pages disjoint" "$([ "$OV" = 0 ]; echo $?)" "$P1 vs $P2"
check "last row is pgu3 (asc)" "$(jq -e --arg u pgu3 '.[-1] == $u' <<<"$P2" >/dev/null; echo $?)" "got: $P2"

# search restricted to selected columns
tot "q on all cols matches tenant" 3 "/api/v1/users?q=$T2"
tot "col=username: no match" 0 "/api/v1/users?q=$T2&col=username"
tot "col=tenant: all match" 3 "/api/v1/users?q=$T2&col=tenant"

# sorting
t "$AJ" "users sort desc" 200 GET "/api/v1/users?q=$T2&sort=username&dir=desc"
HEAD=$(jq -r '.[0].username' /tmp/resp.json)
check "desc head pgu3" "$([ "$HEAD" = pgu3 ]; echo $?)" "got $HEAD"

# bad list params are refused
t "$AJ" "users bad sort" 400 GET "/api/v1/users?sort=password"
t "$AJ" "users bad col" 400 GET "/api/v1/users?col=id"
t "$AJ" "users bad dir" 400 GET "/api/v1/users?dir=sideways"
t "$AJ" "users limit zero" 400 GET "/api/v1/users?limit=0"
t "$AJ" "users offset negative" 400 GET "/api/v1/users?offset=-1"

# fs listing: name search, sort (dirs always first), paging
tot "fs total entries" 4 "/api/v1/fs?path=$PD"
t "$AJ" "fs dirs first" 200 GET "/api/v1/fs?path=$PD"
FN=$(jq -r '.entries[0].entry.name' /tmp/resp.json)
check "sub1 listed first" "$([ "$FN" = sub1 ]; echo $?)" "got $FN"
tot "fs name search q=pa" 1 "/api/v1/fs?path=$PD&q=pa"
t "$AJ" "fs sort size desc" 200 GET "/api/v1/fs?path=$PD&sort=size&dir=desc"
SN=$(jq -r '.entries[1].entry.name' /tmp/resp.json)
check "biggest file after dir" "$([ "$SN" = pc.txt ]; echo $?)" "got $SN"
t "$AJ" "fs page 2" 200 GET "/api/v1/fs?path=$PD&limit=2&offset=2"
N=$(jq '.entries | length' /tmp/resp.json)
check "fs page2 has 2 rows" "$([ "$N" = 2 ]; echo $?)" "got $N"

# grants on one path
for n in 1 2 3; do
  t "$AJ" "grant pgu$n on $PD" 200 PUT /api/v1/perms "{\"path\":\"$PD\",\"principal_type\":\"user\",\"principal\":\"pgu$n\",\"tenant\":\"$T2\",\"read\":true}"
done
tot "perms total grants" 3 "/api/v1/perms?path=$PD"
t "$AJ" "perms limit=2" 200 GET "/api/v1/perms?path=$PD&limit=2"
N=$(jq '.grants | length' /tmp/resp.json)
check "perms page has 2 rows" "$([ "$N" = 2 ]; echo $?)" "got $N"
tot "perms search principal" 1 "/api/v1/perms?path=$PD&q=pgu2"
tot "perms search tenant" 3 "/api/v1/perms?path=$PD&q=$T2&col=tenant"
t "$AJ" "perms sort desc" 200 GET "/api/v1/perms?path=$PD&sort=principal&dir=desc"
HEAD=$(jq -r '.grants[0].principal_name' /tmp/resp.json)
check "perms desc head pgu3" "$([ "$HEAD" = pgu3 ]; echo $?)" "got $HEAD"

# groups and tenants
t "$AJ" "create group pgg" 201 POST /api/v1/groups "{\"tenant\":\"$T2\",\"name\":\"pgg$SFX\"}"
tot "groups search name" 1 "/api/v1/groups?q=pgg$SFX"
tot "tenants search name" 1 "/api/v1/tenants?q=$T2"

# events paging + search (structured filters still apply)
t "$AJ" "events limit=5" 200 GET "/api/v1/events?limit=5"
N=$(jq 'length' /tmp/resp.json)
check "events page of 5" "$([ "$N" = 5 ]; echo $?)" "got $N"
curl -s -o /tmp/resp.json -D /tmp/hdr.txt -b "$AJ" "$BASE_URL/api/v1/events?limit=5&offset=5" >/dev/null
EVT=$(tr -d '\r' < /tmp/hdr.txt | awk -F': ' 'tolower($1)=="x-total-count" {print $2}')
N=$(jq 'length' /tmp/resp.json)
check "events offset page + total" "$([ "$N" = 5 ] && [ "$EVT" -gt 10 ]; echo $?)" "rows $N total $EVT"
t "$AJ" "events q search" 200 GET "/api/v1/events?q=user_create&limit=50"
ALLQ=$(jq -e 'all(.[]; .action == "user_create")' /tmp/resp.json >/dev/null; echo $?)
check "events q filters to matches" "$ALLQ"
t "$AJ" "fs bad sort" 400 GET "/api/v1/fs?path=$PD&sort=perms"
t "$AJ" "perms bad dir" 400 GET "/api/v1/perms?path=$PD&dir=sideways"

echo "== entity meta: description + active filters + tenant kill switch =="
# fresh throwaway tenant/user/group carrying descriptions from birth
t "$AJ" "create tenant w/ description" 201 POST /api/v1/tenants "{\"name\":\"mt$SFX\",\"description\":\"meta test customer\"}"
MTID=$(jq -r '.id' /tmp/resp.json)
check "tenant echoes description+active" "$(jq -e '.description=="meta test customer" and .active==true' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "create user w/ description" 201 POST /api/v1/users "{\"username\":\"mu$SFX\",\"password\":\"pw-$SFX\",\"tenant\":\"mt$SFX\",\"description\":\"q3 contractor\"}"
MUID=$(jq -r '.id' /tmp/resp.json)
check "user echoes description" "$(jq -e '.description=="q3 contractor" and .active==true' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "create group w/ description" 201 POST /api/v1/groups "{\"name\":\"mg$SFX\",\"tenant\":\"mt$SFX\",\"description\":\"build agents\"}"
MGID=$(jq -r '.id' /tmp/resp.json)
check "group echoes description" "$(jq -e '.description=="build agents" and .active==true' /tmp/resp.json >/dev/null; echo $?)"

# description is searchable per column and via the default all-columns search
t "$AJ" "users col=description" 200 GET "/api/v1/users?q=contractor&col=description"
check "col=description finds user" "$(jq -e --arg u "mu$SFX" 'any(.[]; .username==$u)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "users all-columns desc hit" 200 GET "/api/v1/users?q=contractor"
check "default search finds via description" "$(jq -e --arg u "mu$SFX" 'any(.[]; .username==$u)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "groups col=description" 200 GET "/api/v1/groups?q=build%20agents&col=description"
check "groups desc search" "$(jq -e --arg g "mg$SFX" 'any(.[]; .name==$g)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "tenants col=description" 200 GET "/api/v1/tenants?q=meta%20test&col=description"
check "tenants desc search" "$(jq -e --arg t "mt$SFX" 'any(.[]; .name==$t)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "users sort=description" 200 GET "/api/v1/users?sort=description"

# PUT /{id} updates descriptions
t "$AJ" "update user description" 204 PUT "/api/v1/users/$MUID" '{"description":"now permanent"}'
t "$AJ" "update group description" 204 PUT "/api/v1/groups/$MGID" '{"description":"build + release agents"}'
t "$AJ" "update tenant description" 204 PUT "/api/v1/tenants/$MTID" '{"description":"meta test customer renamed"}'
t "$AJ" "user desc update visible" 200 GET "/api/v1/users?q=permanent&col=description"
check "user desc update searchable" "$(jq -e --arg u "mu$SFX" 'any(.[]; .username==$u)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "update unknown user" 404 PUT "/api/v1/users/99999999" '{"description":"x"}'

# active=true|exact filter on entity lists; 400 for invalid values or unsupported lists
t "$AJ" "suspend meta user" 200 POST "/api/v1/users/$MUID/active" '{"active":false}'
t "$AJ" "users active=true" 200 GET "/api/v1/users?active=true"
check "suspended user absent from active=true" "$(jq -e --arg u "mu$SFX" 'all(.[]; .username!=$u)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "users active=false" 200 GET "/api/v1/users?active=false"
check "suspended user present in active=false" "$(jq -e --arg u "mu$SFX" 'any(.[]; .username==$u and .active==false)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "suspend meta group" 200 POST "/api/v1/groups/$MGID/active" '{"active":false}'
t "$AJ" "groups active=false" 200 GET "/api/v1/groups?active=false"
check "suspended group in active=false" "$(jq -e --arg g "mg$SFX" 'any(.[]; .name==$g and .active==false)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "users active invalid" 400 GET "/api/v1/users?active=maybe"
t "$AJ" "fs active unsupported" 400 GET "/api/v1/fs?path=/&active=true"
t "$AJ" "events active unsupported" 400 GET "/api/v1/events?active=true"

# tenant=<name> exact scope on users/groups lists (the grant/member form
# pickers depend on it); an unknown tenant is a valid empty filter, and
# lists without the capability reject the parameter.
t "$AJ" "users tenant filter" 200 GET "/api/v1/users?tenant=mt$SFX"
check "tenant filter only returns that tenant" "$(jq -e --arg t "mt$SFX" 'all(.[]; .tenant==$t)' /tmp/resp.json >/dev/null; echo $?)"
check "tenant filter includes meta user" "$(jq -e --arg u "mu$SFX" 'any(.[]; .username==$u)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "groups tenant filter" 200 GET "/api/v1/groups?tenant=mt$SFX"
check "groups tenant filter only own tenant" "$(jq -e --arg t "mt$SFX" 'all(.[]; .tenant==$t)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "tenant filter unknown" 200 GET "/api/v1/users?tenant=no-such-tenant"
check "unknown tenant filter is empty" "$(jq -e 'length==0' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "tenant filter combines with q" 200 GET "/api/v1/users?tenant=mt$SFX&q=mu"
check "combined filter still scoped" "$(jq -e --arg t "mt$SFX" 'all(.[]; .tenant==$t)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "perms tenant filter unsupported" 400 GET "/api/v1/perms?path=/&tenant=x"
t "$AJ" "fs tenant filter unsupported" 400 GET "/api/v1/fs?path=/&tenant=x"

# tenant-wide kill switch: logins die, live sessions die, rows survive
t "$AJ" "reinstate meta user" 200 POST "/api/v1/users/$MUID/active" '{"active":true}'
t "$BJ" "login meta user" 200 POST /api/v1/auth/login "{\"tenant\":\"mt$SFX\",\"username\":\"mu$SFX\",\"password\":\"pw-$SFX\"}"
t "$BJ" "meta user session live" 403 GET "/api/v1/fs?path=/" # 403 not 401 = authenticated, just ungranted
t "$AJ" "suspend tenant" 204 POST "/api/v1/tenants/$MTID/active" '{"active":false}'
t "$BJ" "session dies on tenant suspend" 401 GET "/api/v1/fs?path=/"
t - "login in suspended tenant" 401 POST /api/v1/auth/login "{\"tenant\":\"mt$SFX\",\"username\":\"mu$SFX\",\"password\":\"pw-$SFX\"}"
t "$AJ" "tenants active=false" 200 GET "/api/v1/tenants?active=false"
check "suspended tenant in active=false" "$(jq -e --arg t "mt$SFX" 'any(.[]; .name==$t and .active==false)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "suspended tenant users intact" 200 GET "/api/v1/users?q=mu$SFX"
check "tenant suspend leaves user rows" "$(jq -e --arg u "mu$SFX" 'any(.[]; .username==$u)' /tmp/resp.json >/dev/null; echo $?)"
# footgun guard: the caller cannot suspend the tenant they stand in
t "$AJ" "find own tenant" 200 GET "/api/v1/tenants?q=$ADMIN_TENANT&col=name"
STID=$(jq -r '.[0].id' /tmp/resp.json)
t "$AJ" "self-tenant suspend refused" 422 POST "/api/v1/tenants/$STID/active" '{"active":false}'
# reinstatement brings logins back
t "$AJ" "reinstate tenant" 204 POST "/api/v1/tenants/$MTID/active" '{"active":true}'
t "$BJ" "login after reinstate" 200 POST /api/v1/auth/login "{\"tenant\":\"mt$SFX\",\"username\":\"mu$SFX\",\"password\":\"pw-$SFX\"}"

# new audit actions land in the forensic trail
t "$AJ" "events user_update" 200 GET "/api/v1/events?action=user_update"
check "user_update event recorded" "$(jq -e --arg u "mu$SFX" 'any(.[]; .target_name==$u)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "events group_update" 200 GET "/api/v1/events?action=group_update"
check "group_update event recorded" "$(jq -e --arg g "mg$SFX" 'any(.[]; .target_name==$g)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "events tenant_update" 200 GET "/api/v1/events?action=tenant_update"
check "tenant_update event recorded" "$(jq -e --argjson t "$MTID" 'any(.[]; .target_id==$t)' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "events tenant_active" 200 GET "/api/v1/events?action=tenant_active"
check "tenant_active event recorded" "$(jq -e --argjson t "$MTID" 'any(.[]; .target_id==$t)' /tmp/resp.json >/dev/null; echo $?)"

t "$AJ" "delete meta tenant" 204 DELETE "/api/v1/tenants/$MTID"

echo "== built-in /help docs tree (user guide embedded in the binary) =="
t "$AJ" "list /help" 200 GET "/api/v1/fs?path=/help"
check "help lists the hub" "$(jq -e 'any(.entries[]; .entry.name=="README.md")' /tmp/resp.json >/dev/null; echo $?)"
check "help lists the guides" "$(jq -e 'any(.entries[]; .entry.name=="api.md") and any(.entries[]; .entry.name=="permissions.md")' /tmp/resp.json >/dev/null; echo $?)"
check "help entries are read-only bits" "$(jq -e 'all(.entries[]; .perms.read==true and .perms.write==false and .perms.delete==false)' /tmp/resp.json >/dev/null; echo $?)"
t "$UJ" "plain user lists /help too" 200 GET "/api/v1/fs?path=/help"
t "$AJ" "read help hub" 200 GET "/api/v1/fs/content?path=/help/README.md"
check "help hub is non-empty text" "$(jq -e '(.content|length) > 100 and .is_binary==false' /tmp/resp.json >/dev/null; echo $?)"
t "$UJ" "plain user reads a guide" 200 GET "/api/v1/fs/content?path=/help/permissions.md"
t "$AJ" "help raw download" 200 GET "/api/v1/fs/raw?path=/help/api-keys.md"
t "$AJ" "help missing doc" 404 GET "/api/v1/fs/content?path=/help/nope-$SFX.md"
t "$AJ" "help missing dir" 404 GET "/api/v1/fs?path=/help/nope-$SFX"
t "$AJ" "help search finds api.md" 200 GET "/api/v1/fs?path=/help&q=api"
check "help search filtered" "$(jq -e 'all(.entries[]; (.entry.name|ascii_downcase)|contains("api"))' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "help mkdir refused" 403 POST /api/v1/fs/mkdir '{"path":"/help/sub"}'
t "$AJ" "help write refused" 403 POST /api/v1/fs/write '{"path":"/help/README.md","content":"x"}'
t "$AJ" "help delete refused" 403 DELETE "/api/v1/fs?path=/help/README.md"
t "$AJ" "help rename refused" 403 POST /api/v1/fs/rename '{"from":"/help/README.md","to":"/README.md"}'
GOT=$(curl -s -o /dev/null -w '%{http_code}' -b "$AJ" -X POST -F "path=/help" -F "file=@/dev/null" "$BASE_URL/api/v1/fs/upload")
check "help upload refused (403)" "$([ "$GOT" = "403" ]; echo $?)"
t "$AJ" "help grant refused" 403 PUT /api/v1/perms '{"path":"/help","principal_type":"user","principal":"nobody","read":true}'
t "$AJ" "help perms show" 200 GET "/api/v1/perms?path=/help/README.md"
check "help perms: read-only, no grants" "$(jq -e '.effective.read==true and .effective.write==false and (.grants|length)==0' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "help draft get is null" 200 GET "/api/v1/fs/draft?path=/help/README.md"
check "no draft for help docs" "$(jq -e '.draft==null' /tmp/resp.json >/dev/null; echo $?)"
t "$AJ" "help draft save refused" 403 PUT /api/v1/fs/draft '{"path":"/help/README.md","content":"x"}'
t "$AJ" "help draft discard refused" 403 DELETE "/api/v1/fs/draft?path=/help/README.md"
# only the exact /help prefix is special; look-alike names stay on disk
t "$AJ" "helpshadow mkdir ok" 201 POST /api/v1/fs/mkdir "{\"path\":\"/helpshadow-$SFX\"}"
t "$AJ" "helpshadow delete ok" 204 DELETE "/api/v1/fs?path=/helpshadow-$SFX"

echo "== session lifecycle =="
t "$UJ" "logout" 204 POST /api/v1/auth/logout
t "$UJ" "fs after logout" 401 GET /api/v1/fs?path=/

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
