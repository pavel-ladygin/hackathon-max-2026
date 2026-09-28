# MAX Together frontend

React/Vite Mini App for event discovery and a two-person shared choice flow. The app calls the Go API for MAX bootstrap, home feed, search, event details, saved events, preferences, and rooms. A room creator can share the invitation through MAX or copy its URL; the invite token is also passed as a MAX `startapp` deep link. Tickets are opened on the event provider's HTTPS page; the app does not sell or issue tickets.

## Run locally

Use the repository-root submission stack so the API and its isolated fixture catalog are available:

```sh
cd ..
docker compose --env-file .env.submission.example build
docker compose --env-file .env.submission.example up -d
```

Frontend: `http://localhost:8081`; API: `http://localhost:8080/api/v1`. This sample includes a dummy bot token, so real MAX signed init data will not authenticate. For a real MAX session, configure an authorized bot token and use the HTTPS Mini App configured for that bot. A regular browser opens the guidance page instead of authenticating as a MAX user.

For frontend-only development, install dependencies with `npm ci`, copy `.env.example` to `.env.local`, and run `npm run dev`. The dev server still needs a reachable Go API. Its defaults are `/api/v1`, the MAX app URL, and the reviewed ticket-provider allowlist.

## Data and external services

The default Compose stack loads the small deterministic local submission fixture after migrations. Those events are identified as `source=submission-fixture` and `is_demo=false` so they exercise the runtime discovery and room filters, but their ticket URLs use the reserved `tickets.example.invalid` domain and are demonstration links only. They are not real provider listings or purchasable tickets.

Live KudaGo synchronization is opt-in with the `live` Compose profile. Set `TIMEPAD_TOKEN` to enable Timepad ingestion as well. Both frontend and backend enforce their ticket URL allowlists; keep them aligned. `VITE_YANDEX_MAPS_API_KEY` is optional and should be restricted to the app's HTTPS origin.

## API contract and structure

`../openapi/openapi.yaml` is the canonical OpenAPI 3.1 contract. Generated TypeScript API types are in `src/shared/api/generated/schema.ts`; regenerate with `npm run generate:api`. The app obtains MAX init data from the platform bridge and submits it to the backend for signature and freshness checks. The backend issues a fresh opaque 24-hour app session for each successful bootstrap. The client keeps the session token in memory; it does not persist raw init data as an app credential.

Main routes are declared in `src/app/App.tsx`: home, events, event detail, saved items, preferences, room creation/invitation/join, and room flow. The share and clipboard integration is implemented by the MAX platform adapter and `InvitePage` in `src/pages/rooms/RoomPages.tsx`.

## Checks

```sh
npm run check
npx playwright install chromium
npm run test:e2e
```

`npm run check` runs provider-policy verification, TypeScript, ESLint, Vitest, and production build. `npm run test:motion` is an additional animation regression suite and is not part of the standard CI workflow.
