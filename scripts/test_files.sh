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

echo "== forensic event trail =="
t "$AJ" "grant list for revoke" 200 GET "/api/v1/perms?path=/hide-$SFX"
GPID=$(jq -r '.grants[0].id' /tmp/resp.json)
t "$AJ" "revoke grant (DML to record)" 204 DELETE "/api/v1/perms/$GPID"
t "$AJ" "events listed" 200 GET "/api/v1/events?limit=500"
for a in tenant_create tenant_delete user_create group_create member_add perm_set perm_delete factor_create factor_delete user_active login_failed; do
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

echo "== session lifecycle =="
t "$UJ" "logout" 204 POST /api/v1/auth/logout
t "$UJ" "fs after logout" 401 GET /api/v1/fs?path=/

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
