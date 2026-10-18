#!/usr/bin/env bash
# API smoke tests. Usage: BASE_URL=http://localhost:8080 ./scripts/test_api.sh
set -u
BASE_URL="${BASE_URL:-http://localhost:8080}"
PASS=0; FAIL=0
JQ() { echo "$1" | jq -r "$2" 2>/dev/null; }

t() { # t <name> <expected_status> <method> <path> [json-body]
  local name=$1 want=$2 method=$3 path=$4 body=${5:-}
  local args=(-s -o /tmp/resp.json -w '%{http_code}' -X "$method" "$BASE_URL$path" -H 'Content-Type: application/json')
  [ -n "$body" ] && args+=(-d "$body")
  local got; got=$(curl "${args[@]}")
  if [ "$got" = "$want" ]; then
    PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-40s %s %s -> %s\n' "$name" "$method" "$path" "$got"
  else
    FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-40s %s %s -> got %s want %s\n' "$name" "$method" "$path" "$got" "$want"
    [ -s /tmp/resp.json ] && sed 's/^/       /' /tmp/resp.json
  fi
}

echo "== health =="
t "healthz"                    200 GET  /healthz

echo "== create =="
t "create gpt-4o"              201 POST /api/v1/llms '{"name":"gpt-4o","provider":"openai","model_id":"gpt-4o","context_length":128000,"enabled":true,"tags":["flagship"]}'
t "create llama3"              201 POST /api/v1/llms '{"name":"llama3-8b","provider":"ollama","model_id":"llama3:8b","endpoint":"http://localhost:11434","context_length":8192,"enabled":true}'
t "create claude"              201 POST /api/v1/llms '{"name":"claude-3-5-sonnet","provider":"anthropic","model_id":"claude-3-5-sonnet","context_length":200000,"enabled":true}'
ID=$(jq -r '.id' /tmp/resp.json)

t "missing name"               422 POST /api/v1/llms '{"provider":"x","model_id":"y"}'
t "missing provider"           422 POST /api/v1/llms '{"name":"noprovider","model_id":"y"}'
t "duplicate name"             409 POST /api/v1/llms '{"name":"gpt-4o","provider":"openai","model_id":"dup"}'
t "negative context"           422 POST /api/v1/llms '{"name":"neg","provider":"p","model_id":"m","context_length":-1}'
t "empty body"                 400 POST /api/v1/llms ''
t "malformed json"             400 POST /api/v1/llms '{oops'
t "unknown field"              400 POST /api/v1/llms '{"name":"x","provider":"p","model_id":"m","bogus":1}'
t "wrong field type"           400 POST /api/v1/llms '{"name":"x","provider":"p","model_id":"m","context_length":"big"}'

echo "== read =="
t "list all"                   200 GET  /api/v1/llms
TOTAL=$(jq -r '.total' /tmp/resp.json)
if [ "$TOTAL" = 3 ]; then PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m %-40s total==3\n' 'list total'
else FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m %-40s total==%s want 3\n' 'list total' "$TOTAL"; fi
t "filter provider=ollama"     200 GET  '/api/v1/llms?provider=ollama'
N=$(jq -r '.total' /tmp/resp.json); [ "$N" = 1 ] && PASS=$((PASS+1)) && echo "  PASS filter ollama==1" || { FAIL=$((FAIL+1)); echo "  FAIL filter ollama==$N want 1"; }
t "search q=claude"            200 GET  '/api/v1/llms?q=claude'
t "get by id"                  200 GET  "/api/v1/llms/$ID"
t "get missing id"             404 GET  /api/v1/llms/does-not-exist

echo "== update =="
t "put full update"            200 PUT  "/api/v1/llms/$ID" '{"name":"claude-3-5-sonnet","provider":"anthropic","model_id":"claude-3-5-sonnet-20241022","context_length":200000,"enabled":false}'
EN=$(jq -r '.enabled' /tmp/resp.json); [ "$EN" = false ] && PASS=$((PASS+1)) && echo "  PASS put set enabled=false" || { FAIL=$((FAIL+1)); echo "  FAIL put enabled==$EN"; }
t "patch partial"              200 PATCH "/api/v1/llms/$ID" '{"enabled":true,"tags":["flagship","vision"]}'
t "patch invalid name"         422 PATCH "/api/v1/llms/$ID" '{"name":"   "}'
t "patch duplicate name"       409 PATCH "/api/v1/llms/$ID" '{"name":"GPT-4O"}'
t "put missing id"             404 PUT  /api/v1/llms/nope '{"name":"a","provider":"p","model_id":"m"}'
t "put keeps created_at"       200 GET  "/api/v1/llms/$ID"
CA=$(jq -r '.created_at' /tmp/resp.json); UA=$(jq -r '.updated_at' /tmp/resp.json)
[ "$CA" != "$UA" ] && PASS=$((PASS+1)) && echo "  PASS created_at != updated_at" || { FAIL=$((FAIL+1)); echo "  FAIL created_at==updated_at"; }

echo "== delete =="
t "delete claude"              204 DELETE "/api/v1/llms/$ID"
t "get deleted"                404 GET  "/api/v1/llms/$ID"
t "delete again"               404 DELETE "/api/v1/llms/$ID"

echo "== misc =="
t "method not allowed"         405 POST /api/v1/llms/any-id
t "unknown route"              404 GET  /api/v1/nope

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" -eq 0 ]
