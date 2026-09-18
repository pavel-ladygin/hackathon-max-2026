# hackathon-max-2026

## Backend development

Requirements: Docker Desktop/Engine with Compose v2; Go 1.24 for the backend.
Regenerating OpenAPI types needs Go 1.25+ (the generator runs separately from the
application module). Contract validation needs Python 3.10+ and pip.

### Local stack

Copy `.env.example` to `.env` to customize local settings. Set `MAX_BOT_TOKEN`
in your shell to your development bot credential (never commit it). From this directory:

```sh
docker compose up -d postgres
docker compose run --rm --build migrate
docker compose run --rm --build --service-ports -e MAX_BOT_TOKEN -e MAX_INIT_DATA_MAX_AGE=5m backend
curl http://localhost:8080/api/v1/health/ready
```

These commands start PostgreSQL 17, apply embedded goose migrations, then start
the backend. Database defaults use local-only development credentials. The shared
Compose file does not forward auth variables yet, so the backend run command
passes them explicitly. The backend and database
ports bind to localhost. Readiness returns HTTP 200 only when PostgreSQL is
reachable and the complete applied migration set matches the binary:

```json
{"status":"ready","database":"ready","migrations":"current"}
```

The backend serves readiness and `POST /api/v1/auth/max/bootstrap` (phase A1).
Other business endpoints in OpenAPI remain contracts for subsequent implementation.

To reset this project's local database (deletes its stored data):

```sh
docker compose down -v
# Repeat the startup commands above.
```

### Run Go directly

Start PostgreSQL with `docker compose up -d postgres`. Export these environment
variables in your shell; the Go process does not load `.env` automatically:

```sh
export APP_ENV=local
export HTTP_ADDR=:8080
export DATABASE_URL='postgres://max_together:local-dev-only@localhost:5432/max_together?sslmode=disable'
export LOG_LEVEL=info
# Set MAX_BOT_TOKEN securely in this shell before starting the server.
export MAX_INIT_DATA_MAX_AGE=5m
cd backend
go run ./cmd/migrate
go run ./cmd/server
```

PowerShell uses `$env:APP_ENV='local'` (and the equivalent for each variable).
Stop the Compose backend first if it already occupies port 8080.
`cmd/migrate` applies the versioned SQL embedded in its binary; it can be rerun
safely. Rebuild the binary/image after adding a migration. Schema changes require
coordination between both backend owners; new migrations use the next free number.

### Generate SQL access code

From `backend`:

```sh
go run github.com/sqlc-dev/sqlc/cmd/sqlc@v1.29.0 generate
```

Alternatively, use the pinned official image from `backend` (POSIX shell):

```sh
docker run --rm -v "${PWD}:/src" -w /src sqlc/sqlc:1.29.0 generate
```

In PowerShell use `-v "${PWD}:/src"` with the same command.

`sqlc.yaml` has two independent outputs: `internal/store/platform` for Backend A
and `internal/store/rooms` for Backend B. Both use `migrations` as their schema.
Each owner edits their own `queries/*.sql`; generated Go files are committed.
Platform includes health and A1 user/session queries; rooms retains its health query.

### Validate and generate OpenAPI

`openapi/openapi.yaml` is the canonical OpenAPI 3.1 contract. From the repository root:

```sh
python -m venv .venv
# Activate .venv in your shell, then:
python -m pip install -r backend/tests/contract/requirements.txt
python -m unittest discover -s backend/tests/contract -v
cd backend
go generate ./internal/httpapi/openapi
```

Validation uses `openapi-spec-validator` plus JSON Schema 2020-12 checks for
examples and local references. `oapi-codegen v2.8.0` produces types only, using
`openapi/oapi-codegen.yaml`. Commit `internal/httpapi/openapi/types.gen.go` after
regeneration. Ordinary builds and tests use the committed files and need no
code-generation tools, Python or Node.

### Tests and shared contracts

```sh
cd backend
go build ./...
go test ./...
go vet ./...
```

Database smoke tests run when `TEST_DATABASE_URL` points to a disposable local
PostgreSQL database (for example the Compose database after migrations); without
it they skip. See `backend/tests/integration` for the exact exercised boundaries.

### A1 authentication

MAX signatures follow the [official validation algorithm](https://dev.max.ru/docs/webapps/validation):
percent-decode values once, exclude `hash`, sort keys, join `key=value` with newlines,
derive the HMAC-SHA256 key from `WebAppData` and the bot token, then verify the
payload HMAC in constant time. Literal `+` remains a plus, following MAX's
`decodeURIComponent` example. Duplicate parameters, malformed identity, future
timestamps and init data older than `MAX_INIT_DATA_MAX_AGE` (default 5m) are rejected.

Bootstrap atomically upserts the user and inserts a 24h session. The response uses
an opaque random 256-bit bearer token; PostgreSQL stores only its SHA-256 hash.
Repeated login preserves the internal UUID and app-owned city/onboarding state.
`preferences` and `invite_context` are `null` in A1; their domain integration is
deferred. A supplied non-null `start_param` hint must match the signed value or
the request returns `400 VALIDATION_FAILED`. No invite lookup occurs in A1.

Backend B can register protected Chi routes with `authService.Middleware` and
read the existing `contracts.PrincipalFromContext`; only the internal UUID crosses
this boundary. Unknown/revoked tokens return `401 UNAUTHENTICATED`, expired tokens
return `401 TOKEN_EXPIRED`, and database errors return a generic `500 INTERNAL`.
The public bootstrap route limits requests to 20/minute/connection-peer IP per
process and returns `429 RATE_LIMITED` with `Retry-After`. Forwarded IP headers
are not trusted; reverse-proxy deployments must arrange trusted client-IP rate
limiting at the edge. No credentials or raw init data are logged or persisted.

`cmd/server` requires `MAX_BOT_TOKEN`; `cmd/migrate` does not. Session TTL is fixed
at 24h by the HTTP contract. Init-data freshness remains configurable for MAX
resume-scenario testing. Integration tests use only synthetic bot credentials
and the existing `TEST_DATABASE_URL` infrastructure.

`internal/contracts` defines the internal UUID principal, `PoolBuilder`,
`EventAvailability` and `BehaviorRecorder`. A computes ordered candidates and
safe snapshots; B persists pools and changes room state. B passes its current
`pgx.Tx` as `store.DBTX` when recording server behavior, so both changes commit
atomically. An MVP room candidate needs published status, available tickets and
a ticket/reservation URL; a zero price alone does not establish eligibility.
