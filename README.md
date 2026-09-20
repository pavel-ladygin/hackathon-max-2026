# hackathon-max-2026

## Backend development

Requirements: Docker Desktop/Engine with Compose v2; Go 1.24 for the backend.
Regenerating OpenAPI types needs Go 1.25+ (the generator runs separately from the
application module). Contract validation needs Python 3.10+ and pip.

### Local stack

From this directory, create your local configuration once (keep an existing `.env`):

```sh
cp .env.example .env
# Manually set MAX_BOT_TOKEN in .env before starting.
docker compose up --build
curl http://localhost:8080/api/v1/health/ready
```

Compose starts PostgreSQL 17, applies embedded goose migrations, then starts
the backend. It passes `MAX_BOT_TOKEN` and `MAX_INIT_DATA_MAX_AGE` from your shell
or local `.env` to the backend. A missing or empty token produces a clear Compose
error; the max-age default is `1h`. Never commit `.env`. Database defaults use
local-only development credentials. The backend and database
ports bind to localhost. Readiness returns HTTP 200 only when PostgreSQL is
reachable and the complete applied migration set matches the binary:

```json
{"status":"ready","database":"ready","migrations":"current"}
```

The backend serves readiness and `POST /api/v1/auth/max/bootstrap`.
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
export MAX_INIT_DATA_MAX_AGE=1h
cd backend
go run ./cmd/migrate
go run ./cmd/server
```

PowerShell uses `$env:APP_ENV='local'` (and the equivalent for each variable).
Stop the Compose backend first if it already occupies port 8080.
`cmd/migrate` applies the versioned SQL embedded in its binary; it can be rerun
safely. Rebuild the binary/image after adding a migration. Schema changes require
coordination between both backend owners; new migrations use the next free number.

### Demo catalog

After exporting the local environment above, run from `backend`:

```sh
go run ./cmd/migrate
# Optional: pin a date for an exactly reproducible catalog.
export DEMO_BASE_DATE=2026-09-18
go run ./cmd/seed
```

PowerShell uses `$env:DEMO_BASE_DATE='2026-09-18'`. The seed reads process
environment only and requires `APP_ENV=local` or `test` and `DATABASE_URL`;
it does not need a bot token. If `DEMO_BASE_DATE` is omitted, it resolves once
to today's date in `Europe/Moscow` and prints the effective date. Event starts
are 1–20 days after that date. Use today's date (or omit the variable) for a
fresh demo; an explicitly pinned past date is intentionally replayed unchanged.

The catalog contains Moscow, 12 metro stations, 12 illustrative venues, 11
category slugs, 44 events and 44 images. Every event has one primary category;
some also have a secondary category. Data includes free, unknown and paid
prices, all event statuses and loudness levels, and available/unavailable demo
ticket states. All events carry `source=demo` and `is_demo=true`. Ticket URLs
use the reserved `tickets.example.invalid` host and cannot sell real tickets.

UUIDs derive from fixed fixture keys (Moscow has a fixed UUID), independently
of dates. One transaction serializes seed runs and reconciles only these demo
fixtures; identical base dates reproduce timestamps, changing the base date
refreshes the same event IDs without duplicates. Schema migrations stay separate.
The backend image also includes `/app/seed`, which accepts the same environment.

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

`sqlc.yaml` has two independent outputs: `internal/store/platform` for platform/auth
data and `internal/store/rooms` for room/matching data. Both use `migrations` as their schema.
Each owner edits their own `queries/*.sql`; generated Go files are committed.
The platform package includes health, user/session, catalog reads and demo seed queries.
The rooms package retains its health query and contains the transaction
and persistence primitives described in
[`backend/internal/rooms/README.md`](backend/internal/rooms/README.md).

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
Room persistence integration tests use the same variable to exercise row locks,
membership constraints, round-scoped persistence, immutable votes/matches and
transaction rollback on real PostgreSQL. These are repository tests; room HTTP
handlers and the two-client release gate belong to subsequent phases.

### Authentication

MAX signatures follow the [official validation algorithm](https://dev.max.ru/docs/webapps/validation):
percent-decode values once, exclude `hash`, sort keys, join `key=value` with newlines,
derive the HMAC-SHA256 key from `WebAppData` and the bot token, then verify the
payload HMAC in constant time. Literal `+` remains a plus, following MAX's
`decodeURIComponent` example. Duplicate parameters, malformed identity, future
timestamps and init data older than `MAX_INIT_DATA_MAX_AGE` (default 1h) are rejected.

Bootstrap atomically upserts the user and inserts a 24h session. The response uses
an opaque random 256-bit bearer token; PostgreSQL stores only its SHA-256 hash.
Repeated login preserves the internal UUID and app-owned city/onboarding state.
`preferences` and `invite_context` are currently `null`; their domain integration is deferred.
A supplied non-null `start_param` hint must match the signed value or
the request returns `400 VALIDATION_FAILED`. No invite lookup occurs at this stage.

Protected room routes can reuse `authService.Middleware` and
`contracts.PrincipalFromContext`; only the internal UUID crosses
this boundary. Unknown/revoked tokens return `401 UNAUTHENTICATED`, expired tokens
return `401 TOKEN_EXPIRED`, and database errors return a generic `500 INTERNAL`.
The public bootstrap route limits requests to 20/minute/client IP per process
and returns `429 RATE_LIMITED` with `Retry-After`. Forwarded IP headers are used
only when the direct peer belongs to `TRUSTED_PROXY_CIDRS`; the default is empty,
so local development keeps trusting the connection peer only. Production Nginx
also applies an edge limit. No credentials or raw init data are logged or persisted.

`cmd/server` requires `MAX_BOT_TOKEN`; `cmd/migrate` does not. Session TTL is fixed
at 24h by the HTTP contract. Init-data freshness remains configurable for MAX
resume-scenario testing. Integration tests use only synthetic bot credentials
and the existing `TEST_DATABASE_URL` infrastructure.

`internal/contracts` defines the internal UUID principal, `PoolBuilder`,
`EventAvailability` and `BehaviorRecorder`. The recommendation layer computes
ordered candidates and safe snapshots; the rooms layer persists pools and owns
room state transitions. When recording server behavior, room transactions pass
their current `pgx.Tx` as `store.DBTX`, so both changes commit atomically. An MVP room candidate needs published status, available tickets and
a ticket/reservation URL; a zero price alone does not establish eligibility.

## Production deployment

Production is served at `https://worknet.team`; the public API base URL is
`https://worknet.team/api/v1`. Host Nginx terminates TLS and proxies only to the
loopback bindings of the frontend (`127.0.0.1:8081`) and backend
(`127.0.0.1:8080`). PostgreSQL has no host port.

The deployed frontend is temporarily built with `VITE_API_MODE=mock`, so the
complete demonstration flow uses the explicitly marked MSW demo dataset while
the backend business endpoints are still being implemented. The public backend
and readiness endpoint remain deployed at `/api/v1`. Switching production to the
real API later requires changing the frontend image build argument to `http` and
passing the full release gate against the implemented API.

The deployment assets are:

- `compose.production.yaml` for PostgreSQL, migrations, backend and frontend;
- `deploy/nginx/` for the host Nginx configuration;
- `deploy/bootstrap-vps.sh` for one-time Ubuntu package/firewall setup;
- `deploy/enable-https.sh` for initial certificate issuance and final Nginx setup;
- `deploy/deploy.sh <40-character-commit-sha>` for backup, migration, rollout,
  public smoke checks and application-image rollback.

Create `/opt/worknet/.env.production` directly on the server with mode `0600`.
It must contain at least `POSTGRES_PASSWORD` and `MAX_BOT_TOKEN`; it may also set
`POSTGRES_DB`, `POSTGRES_USER`, `LOG_LEVEL`, `MAX_INIT_DATA_MAX_AGE` and
`TRUSTED_PROXY_CIDRS`. Never commit this file. Authenticate Docker to GHCR with a
token limited to `read:packages`.

After DNS resolves to the VPS, replace the temporary HTTP-only site with the
certificate-backed production site and verify renewal with:

```sh
sudo ./deploy/enable-https.sh
```

GitHub Actions runs checks for pull requests and pushes to `main` or `dev`.
Only a successful push to `main` publishes commit-SHA images and calls the VPS
deployment script. Required GitHub environment secrets are `DEPLOY_HOST`,
`DEPLOY_USER`, `DEPLOY_SSH_KEY` and a pinned `DEPLOY_HOST_KEY` known-hosts line.
The manual rollback workflow accepts a previously published full commit SHA.

The runtime currently exposes readiness and MAX bootstrap. Do not present the
remaining OpenAPI operations as deployed checks until their handlers are
implemented; add the hackathon `DATA-API.yaml` only after that mandatory scenario
and the evaluator's exact configuration schema are fixed.
