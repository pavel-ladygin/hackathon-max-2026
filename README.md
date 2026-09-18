# hackathon-max-2026

## Backend development

Requirements: Docker Desktop/Engine with Compose v2; Go 1.24 for the backend.
Regenerating OpenAPI types needs Go 1.25+ (the generator runs separately from the
application module). Contract validation needs Python 3.10+ and pip.

### Local stack

Copy `.env.example` to `.env` to customize local settings, then from this directory:

```sh
docker compose up --build
curl http://localhost:8080/api/v1/health/ready
```

Compose starts PostgreSQL 17, applies the embedded goose migrations through a
one-shot `migrate` service, then starts the backend. Defaults work without an
`.env` file and use local-only development credentials. The backend and database
ports bind to localhost. Readiness returns HTTP 200 only when PostgreSQL is
reachable and the complete applied migration set matches the binary:

```json
{"status":"ready","database":"ready","migrations":"current"}
```

The backend serves only readiness at this stage. Business endpoints in OpenAPI
are contracts for subsequent implementation.

To reset this project's local database (deletes its stored data):

```sh
docker compose down -v
docker compose up --build
```

### Run Go directly

Start PostgreSQL with `docker compose up -d postgres`. Export these environment
variables in your shell; the Go process does not load `.env` automatically:

```sh
export APP_ENV=local
export HTTP_ADDR=:8080
export DATABASE_URL='postgres://max_together:local-dev-only@localhost:5432/max_together?sslmode=disable'
export LOG_LEVEL=info
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
The foundation includes only a minimal health query in each package.

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
```

Database smoke tests run when `TEST_DATABASE_URL` points to a disposable local
PostgreSQL database (for example the Compose database after migrations); without
it they skip. See `backend/tests/integration` for the exact exercised boundaries.

`internal/contracts` defines the internal UUID principal, `PoolBuilder`,
`EventAvailability` and `BehaviorRecorder`. A computes ordered candidates and
safe snapshots; B persists pools and changes room state. B passes its current
`pgx.Tx` as `store.DBTX` when recording server behavior, so both changes commit
atomically. An MVP room candidate needs published status, available tickets and
a ticket/reservation URL; a zero price alone does not establish eligibility.
