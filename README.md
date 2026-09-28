# MAX Together

MAX Together is a MAX Mini App for discovering events and choosing one together. A participant can browse and save events, create a private room, invite one other person through MAX or a shareable link, set private preferences, and vote on a shared event pool. A mutual like creates a match. Ticket links open the event provider; this project does not sell tickets, accept payment, or issue tickets.

## Architecture and current data

- `frontend/`: React 19, TypeScript, Vite, MAX UI and MAX Bridge adapter.
- `backend/`: Go HTTP API and workers; PostgreSQL via pgx; versioned Goose migrations.
- `openapi/openapi.yaml`: canonical OpenAPI 3.1 contract used for generated Go and TypeScript types.
- `compose.yaml`: isolated local review stack, migrations, fixture loader, API and frontend. Live provider synchronization is opt-in.
- `compose.production.yaml`: PostgreSQL, migration, API, frontend, event synchronization and daily notifications.
- `compose.maintenance.yaml`: manual Timepad poster recovery service; not part of normal deployment.

Live events are imported from KudaGo and optionally Timepad. The default local review stack loads a fixed small catalog from `backend/seed/submission.json` into the isolated `max_together_submission` database. Those records use `source=submission-fixture` and `is_demo=false` so discovery and room eligibility filters exercise the normal API path. Their `tickets.example.invalid` URLs are non-purchasable demonstration links, not real provider offers. The separate Go demo seed (`go run ./cmd/seed`) creates 44 `is_demo=true` events; normal discovery and room pools exclude those records.

## Run the local review stack

Requirements: Docker Engine/Desktop with Compose v2. From this directory:

```sh
docker compose --env-file .env.submission.example build
docker compose --env-file .env.submission.example up -d
```

The API is at `http://localhost:8080/api/v1`; the frontend is at `http://localhost:8081`. Check readiness and the frontend:

```sh
curl --fail http://localhost:8080/api/v1/health/ready
curl --fail http://localhost:8081/health
```

The Compose stack waits for PostgreSQL, applies migrations, loads the isolated submission fixture, then starts the API and frontend. The example values are disposable local placeholders. `MAX_BOT_TOKEN=submission-only-bot-token` is not a MAX bot credential, so genuine signed MAX init data will not authenticate in this local configuration. Use a bot token authorized for the Mini App and open it through MAX to verify signed user sessions. Do not add real tokens, signed init data, session tokens, passwords or production configuration to the repository.

Stop containers and retain the local database:

```sh
docker compose --env-file .env.submission.example down
```

Remove the isolated database volume as well:

```sh
docker compose --env-file .env.submission.example down -v
```

Compose project name and database are `max-together-submission` and `max_together_submission`; the fixture loader refuses any other database. Compose namespaces the PostgreSQL volume under this project name, so this stack gets a separate volume and leaves the older project's volume untouched. Default ports are 5432, 8080 and 8081. If a port is occupied, change its `*_PORT` value in a copied env file and pass that file with `--env-file`.

### Optional live event imports

The normal review stack does not contact event providers. To enable the periodic importer:

```sh
docker compose --env-file .env.submission.example --profile live up -d event-sync
docker compose --env-file .env.submission.example logs --since=1h event-sync
```

KudaGo sync runs by default; set a real `TIMEPAD_TOKEN` in a private local env file to enable Timepad as well. The worker syncs immediately and then on `EVENT_SYNC_INTERVAL` (default 60 minutes). Provider availability, current event dates and room-eligible counts depend on live source data; fixture presence and container readiness do not prove live catalog availability.

### Optional Timepad poster recovery

Poster recovery is separate from normal local startup and normal production deploy. The utility in `backend/cmd/backfill-timepad-posters` has dry-run behavior unless `--apply` is specified. On the production host, from the directory containing the Compose files and private env file, inspect candidates first:

```sh
docker compose --env-file .env.production -f compose.production.yaml -f compose.maintenance.yaml --profile maintenance run --build --rm timepad-image-recovery
```

Only after reviewing the output, apply a one-shot repair:

```sh
docker compose --env-file .env.production -f compose.production.yaml -f compose.maintenance.yaml --profile maintenance run --build --rm timepad-image-recovery /app/backfill-timepad-posters --apply --limit 50
```

This is a separate manual maintenance deployment action; the optional backfill image target is not built or started by the ordinary deploy path. `TIMEPAD_TOKEN` is required for this command; without it the utility exits with `DATABASE_URL and TIMEPAD_TOKEN are required`, before contacting the provider. Do not enable continuous recovery as a default submission service. The production deployment retains `daily-notifications` and `event-sync`.

## MAX authentication and privacy boundary

For each user, the Mini App sends the current MAX-signed `initData` to `POST /api/v1/auth/max/bootstrap`. The backend verifies the signature and freshness against `MAX_BOT_TOKEN`; it does not trust `initDataUnsafe` as authentication. A successful bootstrap upserts the user and issues a new opaque 24-hour application session. API calls use that session as a Bearer token. The client holds the session in memory. Raw `initData` is neither stored nor treated as a reusable app credential; never put it in a README, DATA-API file, logs, screenshots or a public handoff. Verify the two participants independently with two real MAX identities (A and B).

The user ID/display fields needed to resolve an account are persisted by the application; MAX signature material and the raw bootstrap payload are not. Geolocation coordinates are sent only when the user grants location access and are used for nearby discovery. Ticket provider links are allowlist-checked before use. The app is not a ticket seller, and each external ticket provider owns its purchase, cancellation and refund flow.

### Inspecting MAX bootstrap in Network tools

In an authorized MAX WebView session, inspect the request to `POST /api/v1/auth/max/bootstrap` in the platform's Network tools. Its JSON request has a required `init_data` string copied from the platform bridge, plus an optional `start_param` hint. The response has `access_token`, `token_type: "Bearer"`, `expires_in: 86400`, `user`, `onboarding_state`, `preferences`, `daily_notifications_enabled`, `invite_context`, and `shared_event_id`. A successful bootstrap creates a fresh app session for that user. Do not export HAR files or screenshots with `init_data`, the response `access_token`, or Authorization headers; redact them before sharing diagnostics. `start_param` is trusted only after the backend validates the signed `init_data` and verifies the hint matches it.

### Reproducible local auth and HTTP walkthrough

The following commands use only the **synthetic local** `submission-only-bot-token` in `.env.submission.example`; they do not authenticate with MAX. The helper creates a fresh timestamp and HMAC signature in memory for every bootstrap and emits the JSON body directly to `curl`. Keep the shell session private and close it when finished so its temporary Bearer-token variables are discarded. Never substitute a production token or signed MAX `init_data` into this test helper.

From the repository root, after starting the stack:

```sh
API=http://127.0.0.1:8080/api/v1
CITY_ID=a0f625ee-2154-5a45-8afe-37adf955ec24
FIXTURE_EVENT_ID=ce8f2695-324f-52b0-abba-4de56e9114c1

bootstrap_body() {
  python3 - "$1" "${2:-}" <<'PY'
import hashlib, hmac, json, sys, time, urllib.parse
user_id, start = sys.argv[1], sys.argv[2]
fields = {
    "auth_date": str(int(time.time())),
    "user": json.dumps({"id": int(user_id), "first_name": "Submission"}, separators=(",", ":")),
}
if start:
    fields["start_param"] = start
key = hmac.new(b"WebAppData", b"submission-only-bot-token", hashlib.sha256).digest()
payload = "\n".join(f"{name}={fields[name]}" for name in sorted(fields)).encode()
fields["hash"] = hmac.new(key, payload, hashlib.sha256).hexdigest()
body = {"init_data": urllib.parse.urlencode(fields)}
if start:
    body["start_param"] = start
print(json.dumps(body, separators=(",", ":")))
PY
}

# Fresh local bootstrap for user A; response and access token stay in shell memory.
A_RESPONSE=$(curl --fail --silent --show-error -X POST "$API/auth/max/bootstrap" \
  -H 'Content-Type: application/json' --data "$(bootstrap_body 900000001)")
A_TOKEN=$(printf '%s' "$A_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

# Home, search and detail routes are authenticated. The fixture event ID is stable.
curl --fail --silent --show-error "$API/feed/home?city_id=$CITY_ID" -H "Authorization: Bearer $A_TOKEN"
curl --fail --silent --show-error "$API/events/search?city_id=$CITY_ID&limit=20" -H "Authorization: Bearer $A_TOKEN"
curl --fail --silent --show-error "$API/events/$FIXTURE_EVENT_ID" -H "Authorization: Bearer $A_TOKEN"

# Create room as A; keep room/invite/session tokens only in this shell.
ROOM_RESPONSE=$(curl --fail --silent --show-error -X POST "$API/rooms" \
  -H "Authorization: Bearer $A_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')" \
  --data "{\"name\":\"Submission walkthrough\",\"city_id\":\"$CITY_ID\"}")
ROOM_ID=$(printf '%s' "$ROOM_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["room"]["id"])')
INVITE_TOKEN=$(printf '%s' "$ROOM_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["invite"]["token"])')

# A real UI test shares invite.max_deep_link using MAX share APIs. This direct
# local API walkthrough bootstraps B with the same signed start_param instead.
B_RESPONSE=$(curl --fail --silent --show-error -X POST "$API/auth/max/bootstrap" \
  -H 'Content-Type: application/json' --data "$(bootstrap_body 900000002 "$INVITE_TOKEN")")
B_TOKEN=$(printf '%s' "$B_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
curl --fail --silent --show-error -X POST "$API/room-invites/$INVITE_TOKEN/join" \
  -H "Authorization: Bearer $B_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')" --data '{}'

# Both participants submit a matching future intent, read the pool and like its first card.
EVENT_DAY=$(docker compose --env-file .env.submission.example exec -T postgres psql -U max_together_submission -d max_together_submission -Atc 'SELECT CURRENT_DATE + 7')
INTENT="{\"dates\":[\"$EVENT_DAY\"],\"day_types\":[],\"time_slots\":[],\"category_slugs\":[\"concerts\"],\"budget_max_minor\":300000,\"exclusion_slugs\":[]}"
for TOKEN in "$A_TOKEN" "$B_TOKEN"; do
  curl --fail --silent --show-error -X PUT "$API/rooms/$ROOM_ID/intent/me" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$INTENT"
done
POOL=$(curl --fail --silent --show-error "$API/rooms/$ROOM_ID/events" -H "Authorization: Bearer $A_TOKEN")
POOL_VERSION=$(printf '%s' "$POOL" | python3 -c 'import json,sys; print(json.load(sys.stdin)["pool_version"])')
EVENT_ID=$(printf '%s' "$POOL" | python3 -c 'import json,sys; print(json.load(sys.stdin)["items"][0]["event"]["id"])')
VOTE="{\"pool_version\":$POOL_VERSION,\"vote\":\"like\"}"
for TOKEN in "$A_TOKEN" "$B_TOKEN"; do
  curl --fail --silent --show-error -X PUT "$API/rooms/$ROOM_ID/events/$EVENT_ID/vote" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$VOTE"
done
curl --fail --silent --show-error "$API/rooms/$ROOM_ID" -H "Authorization: Bearer $A_TOKEN"
curl --fail --silent --show-error -X POST "$API/events/$EVENT_ID/ticket-click" \
  -H "Authorization: Bearer $A_TOKEN" -H 'Content-Type: application/json' \
  --data "{\"source\":\"match\",\"room_id\":\"$ROOM_ID\"}"
```

The fixture date is derived from the database's current date plus seven days. If the pool is not ready yet (`409 POOL_NOT_READY`), retry its GET after the response's retry guidance or wait briefly. Fixture ticket-click deliberately resolves only to the reserved `.invalid` host.

### MAX invitation sharing by client

The invitation screen sends the same `{text, link}` payload to `shareMaxContent` when available, falls back to `shareContent`, then to the browser Web Share API. It also offers copy-link as a separate fallback. The URL includes the invitation token; the MAX deep link carries that token in `startapp`. When a user opens a link outside MAX, the UI offers `openMaxLink` then `openLink` where available (and an external-window fallback); it does not treat a normal browser's empty init data as authenticated.

Manual checklist: verify create/share/copy/open/join on a **mobile MAX client** and a **MAX Web client** if available; verify `shareMaxContent` on clients that expose it, `shareContent` fallback where exposed, and browser Web Share/copy in a normal browser separately. On both MAX clients confirm B joins the intended room, sees the shared pool, and can reach the match. A plain browser may test layout/fallback only and cannot perform real MAX auth with the placeholder token.

## Official submission checklist and known blockers

The current technical slide draft still has placeholders for the bot link, repository link and commit SHA; it does not yet contain short run/check steps or a complete required-env list. Before submission, fill and verify these fields:

- working MAX bot/Mini App URL;
- repository URL and the exact 40-character release commit SHA;
- HTTPS API base URL and an external availability check;
- short instructions to open the Mini App and run the main two-user scenario;
- names/roles and access method for test users A and B, if organizers require them;
- required env names and a safe way to supply any real API keys/tokens needed for verification.

The application does not implement username/password test accounts: organizer/API access uses MAX-signed init data followed by a short-lived Bearer session. The organizer-provided test-login/password/role and token/API-key requirements therefore need clarification with the organizers. An official machine-readable DATA-API schema/version has not been provided either. `DATA-API.yaml` is a repository checklist aligned to the current OpenAPI and handlers, not a claim of compatibility with an unknown organizer validator. These access/schema questions are manual submission blockers; do not resolve them by inventing credentials or an official schema.

## API and submission scenario

`DATA-API.yaml` lists the checked API flow: bootstrap users A and B; read the home feed, search events and open an event detail; create a room; share the invitation through MAX or copy its URL/deep link; join as B; set private intents; load the common pool; vote; confirm a mutual-like match; open the provider ticket page. Required checks use existing public routes in `openapi/openapi.yaml`. Bootstrap requires valid, current, signed MAX init data for each test user; bearer session tokens returned by bootstrap are temporary and must not be distributed as reusable test credentials.

Recommended manual review in MAX:

1. Open the app as A, finish onboarding, browse/search and open an event.
2. Save or unsave an event, then create a room and share the invite from MAX.
3. Open the deep link as B and join the room.
4. Set a different private intent for each user, then review the common pool from both devices.
5. Vote on the same event with both users; confirm a match and open the provider link.
6. Reopen the room to confirm state recovery. Also review the empty-pool and provider-failure UI when those conditions are available.

## Environment and service configuration

`.env.submission.example` contains only disposable values for the isolated local stack; use it directly for the commands above or copy it to a private env file. **Never use it for production.** `.env.example` is the general local template. Never edit or replace an existing `.env` without preserving its local secrets. Production secrets belong in `/opt/worknet/.env.production` with mode `0600`, not in this repository.

Required API settings include `APP_ENV`, `HTTP_ADDR`, `DATABASE_URL`, `LOG_LEVEL`, `MAX_BOT_TOKEN`, `INVITE_ENCRYPTION_KEY`, `INVITE_URL_TEMPLATE` and `MAX_DEEP_LINK_TEMPLATE`. The invitation key must be base64 encoding of 32 bytes and remain stable for stored invitations. Each URL template must be HTTPS and contain `{token}` exactly once. The local example uses `.invalid` hostnames intentionally. Optional settings include `TIMEPAD_TOKEN`, `VITE_YANDEX_MAPS_API_KEY`, provider sync endpoints/timeouts and interval. Frontend and backend ticket allowlists must remain aligned; current local allowed ticket domains are `kudago.com`, `*.kudago.com`, `timepad.ru`, `*.timepad.ru`, and fixture-only `tickets.example.invalid`.

Production Compose configuration, provisioned only in `/opt/worknet/.env.production`:

| Variables | Production value / source |
| --- | --- |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | Dedicated production database/user and a strong private password. |
| `BACKEND_IMAGE`, `FRONTEND_IMAGE` | SHA-tagged GHCR images; the CI deploy script sets these from the selected release SHA. |
| `MAX_BOT_TOKEN`, `MAX_APP_URL` | Real MAX bot token and the public Mini App/bot URL. |
| `INVITE_ENCRYPTION_KEY`, `INVITE_ENCRYPTION_KEY_VERSION` | Stable base64 encoding of 32 secret bytes and its key version; do not rotate while stored invitations rely on it. |
| `INVITE_URL_TEMPLATE`, `MAX_DEEP_LINK_TEMPLATE` | HTTPS public invite and MAX deep-link templates, each with exactly one `{token}` placeholder. |
| `TICKET_PROVIDER_ALLOWLIST` | `kudago.com,*.kudago.com,timepad.ru,*.timepad.ru`; omit fixture-only `tickets.example.invalid`. |
| `TIMEPAD_TOKEN` | Optional real provider token; leave unset to use KudaGo only. |
| `TRUSTED_PROXY_CIDRS`, `LOG_LEVEL`, `MAX_INIT_DATA_MAX_AGE` | Set to match the real proxy network and operating policy; `APP_ENV=production` is set by Compose. |

For frontend build, `YANDEX_MAPS_API_KEY` is an optional GitHub production secret passed as a Docker build argument. Never copy the submission example's local credentials or `.invalid` invitation templates into production.

## Existing checks

The CI workflow (`.github/workflows/ci-cd.yml`) runs the established checks. The release review ran the checks listed here; results are recorded below.

Backend, from `backend/` with PostgreSQL 17 migrated and `TEST_DATABASE_URL` set:

```sh
go run ./cmd/migrate
go test -p 1 ./...
go vet ./...
go build ./...
```

The CI uses `-p 1` because backend test packages share a database. Compose's `integration-tests` service is configured to use the same serial package mode. The real-catalog E2E tests are opt-in: `RUN_KUDAGO_REAL_E2E=1` requires imported eligible KudaGo events; `RUN_REAL_PROVIDER_ROOM_E2E=1` requires eligible future events from both KudaGo and Timepad. They are not required for the local fixture-only run.

Contracts and generated types:

```sh
python -m pip install -r backend/tests/contract/requirements.txt
python -m unittest discover -s backend/tests/contract -v
(cd backend && go generate ./internal/httpapi/openapi)
(cd frontend && npm ci && npm run generate:api)
```

Frontend, from `frontend/`:

```sh
npm ci
npm run check
npx playwright install chromium
npm run test:e2e
```

`npm run check` runs provider-policy verification, typecheck, lint, unit tests and production build. `npm run test:motion` is an extra existing animation suite outside standard CI. CI also enables `RUN_GENERIC_BROWSER_E2E=1` for `go test ./internal/eventsources -run '^TestGenericEventSourcesBrowserAgainstLiveHandler$' -count=1`.

Docker/release workflow additionally checks both Compose configurations, builds backend/frontend images, starts the migration/fixture/API/frontend smoke stack, checks API/frontend health, runs `deploy/test-internal-nginx.sh`, scans images with Trivy, and scans the repository with Trufflehog. Deploy/publish occurs on `main` push and on same-repository merged `dev` PRs labeled `deploy-dev`; ordinary `dev` pushes run CI without publishing/deploying. Rollback is a manual workflow using a published full commit SHA.

### Release review results

- Backend: full serial `go test -p 1 -count=1 ./...`, `go vet ./...` and `go build ./...` passed against the isolated submission database. Migration readiness reported version 24 current.
- Generated contracts: OpenAPI Go generation and sqlc v1.29.0 generation passed without inferred Go model type drift after explicit SQL text casts. OpenAPI contract validation passed all 4 cases. The local `DATA-API.yaml` flow matched 13 existing OpenAPI routes/methods/statuses; official organizer schema compatibility remains unknown.
- Frontend: `npm run check` passed 159 tests/checks; browser E2E passed 46 tests. Motion E2E passed 4/4 on repeat after an initial intermittent 3/4 run.
- Runtime smoke: generic live-handler browser acceptance passed (119.658 s); Nginx namespace smoke passed. Restart preserved both fixture records and the matched-room record; readiness stayed healthy.
- Docker: local and production Compose config checks and `linux/amd64` application image builds passed. Trivy 0.70.0 passed all four final backend/frontend ARM64/AMD64 images with the existing CI policy (HIGH/CRITICAL, ignore-unfixed); no matching vulnerabilities were reported. The ARM64 cold/warm measured build details are below; production AMD64 build time was not measured.
- Secret scanning: TruffleHog ran on delivery files with networking and credential verification disabled; its 8 unverified candidates were synthetic PostgreSQL examples and URI test fixtures. Dependencies/build output and `.git` were excluded. Local verified scanning was blocked by automatic approval review because credential verification can send candidates to external provider APIs. The existing verified/full-history CI scan remains required before merge; local Git operations were prohibited during this review.
- Manual limitations: real MAX A/B identities and organizer-provided credentials/schema are still needed for official access validation. Poster recovery is opt-in and needs a real Timepad token.

### Docker build measurement

Measured with the submission env file and the command used for the cold/warm comparison:

```sh
docker compose --env-file .env.submission.example build
```

- Cold: **78.642 s**; warm: **7.116 s**; both completed successfully.
- The final cold run used a fresh Buildx `docker-container` builder with only base-image preparation; Go module download, `npm ci`, compilation and image export were included in the measured build. It ran against final source, including the SQL type casts.
- Host/runtime: Docker 28.5.1, Linux `aarch64` / `linux/arm64`, 8 CPUs, about 3.83 GiB RAM.
- Against the 300-second build limit, the measured ARM64 build passes. Production `linux/amd64` build time has not been measured separately.

## Production deployment

The advertised service is `https://worknet.team`; its API base is `https://worknet.team/api/v1`. Public availability, TLS, bot configuration, provider sync and two-user behavior must be verified against the actual environment before release. Production Nginx terminates TLS and proxies frontend/API to loopback ports; PostgreSQL is not published on the host.

`deploy/deploy.sh <40-character-commit-sha>` backs up PostgreSQL, applies migrations, updates app images and performs public smoke checks with image rollback on failure. It deploys backend, frontend, `event-sync` and `daily-notifications`; poster recovery remains a deliberate maintenance operation. Required GitHub production secrets are `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY` and `DEPLOY_HOST_KEY`; `YANDEX_MAPS_API_KEY` is optional. Configure the production env privately on the server and authorize Docker to pull the release images from GHCR.

If the previous release left a continuous recovery container running, removing it is a separate manual host action; the normal deployment script does not remove it. Review the matching Compose project/service first, then stop and remove only those containers:

```sh
legacy_recovery_ids=$(docker ps -q --filter label=com.docker.compose.project=max-together-production --filter label=com.docker.compose.service=timepad-image-recovery)
if [ -n "$legacy_recovery_ids" ]; then
  docker stop $legacy_recovery_ids
  docker rm $legacy_recovery_ids
fi
```
