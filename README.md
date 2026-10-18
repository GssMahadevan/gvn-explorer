# llm-crud1

REST API for managing LLM entries. Go backend only for now; a web GUI will
follow once the API is stable.

## Run

```sh
GO111MODULE=on go build -o bin/server ./cmd/server
./bin/server -addr :8080          # flags: -db llm.db (or :memory:), -web web (or "")
```

Env vars `ADDR`, `DB_PATH`, `WEB_DIR` set the same defaults; flags win.
An explicitly empty `WEB_DIR=` disables the GUI (then `GET /` is 404).

Web GUI: http://localhost:8080 — list/search/filter, create, edit, delete,
enable toggle; served from `web/` by the same binary (vanilla JS, no build step).

Note: your shell has `GO111MODULE=off` set globally, so prefix `go` commands
with `GO111MODULE=on` (or unset it).

## API

| Method | Path                | Notes                                  |
|--------|---------------------|----------------------------------------|
| GET    | /healthz            | liveness + entry count                 |
| GET    | /api/v1/llms        | filters: `?provider=`, `?enabled=`, `?q=` |
| POST   | /api/v1/llms        | create; 409 on duplicate name          |
| GET    | /api/v1/llms/{id}   |                                        |
| PUT    | /api/v1/llms/{id}   | full replace                           |
| PATCH  | /api/v1/llms/{id}   | partial update (only provided fields)  |
| DELETE | /api/v1/llms/{id}   | 204 on success                         |

Entry fields: `name`*, `provider`*, `model_id`*, `endpoint`,
`context_length`, `enabled`, `tags` (*required). Unknown JSON fields are
rejected with 400. Errors use `{"error":{"code","message"}}`.

```sh
curl -s localhost:8080/api/v1/llms | jq
curl -s -X POST localhost:8080/api/v1/llms \
  -H 'Content-Type: application/json' \
  -d '{"name":"gpt-4o","provider":"openai","model_id":"gpt-4o","context_length":128000,"enabled":true}' | jq
```

## Tests

```sh
GO111MODULE=on go test ./...                       # store suite: memory + sqlite
./bin/server -addr :8090 -db /tmp/t.db &
BASE_URL=http://localhost:8090 ./scripts/test_api.sh
```

32 curl-based assertions covering CRUD, validation (422), duplicates (409),
bad JSON (400), filtering, timestamps, 404/405 paths. Go tests run the same
CRUD/filter suite against both Store implementations.

## Layout

```
cmd/server/        main + middleware (logging, panic recovery)
internal/models/   LLM entity, validation, PATCH merge
internal/store/    Store interface + in-memory & SQLite impls
internal/handlers/ REST handlers, JSON error envelope
web/               single-page GUI (index.html, app.js, style.css)
scripts/test_api.sh curl smoke tests
```
