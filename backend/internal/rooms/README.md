# Backend B rooms (B1 / B2)

B1 supplies transaction-bound repositories, sqlc primitives and the room state
transition graph. B2 implements create-room with persistent idempotency. The
graph alone does not authorize a transition; B3 and later operations are absent.

## Create-room (B2)

`NewCreateService(pool, recorder, inviteCodec)` requires the existing shared
`contracts.BehaviorRecorder` and an `InviteCodec`. `RegisterRoutes` mounts
`POST /api/v1/rooms`; the application must wrap it in the existing auth middleware.
The handler reads only the internal principal from that middleware. The initial
response uses generated `CreateRoomResponse` / `RoomSnapshot` types, contains one
creator, a private creator invite and explicit null intent/pool/match fields.

The service validates a trimmed 1–80 Unicode-character name, city existence and
an 8–128 character idempotency key. It locks the user before checking persistent
idempotency and active membership. An unexpired collecting/ranking/voting room
blocks creation. An exhausted, matched or expired previous room is retired in
the same transaction as the new room, creator membership, round-one state,
invite, server behavior and idempotency record. Retirement deactivates all old
members, clears exact coordinates and expires old invites. A restartable
exhausted room is also expired and its version incremented, preventing restart.

Database wall-clock time is refreshed after lock acquisition, including when a
peer retired the old membership while this request waited. Room/invite TTL and
successful idempotency retention are 48 hours. Within that retention, a matching
user/key/route and normalized payload return the original response with status
201, even if the room subsequently changed. A different payload returns
`IDEMPOTENCY_CONFLICT`. Expired records are replaced only within the transaction;
failures roll back both old-room retirement and new writes. Failed requests are
not cached. The normalization is trimming the name and canonicalizing its city
UUID; JSON whitespace and property order do not affect the hash.

`InviteCodec` generates 256-bit CSPRNG tokens encoded as base64url. Invite lookup
uses SHA-256; recovery uses AES-256-GCM ciphertext bound to the room and key
version. Idempotency responses are also encrypted because they contain the token:
`idempotency_records.response_body` holds a JSON envelope with `key_version` and
`ciphertext`, bound to user/key/route. It is not a plaintext HTTP response.

The codec requires an explicit 32-byte key, positive key version and two URL
templates containing `{token}` exactly once outside the authority. HTTPS is
required except for loopback HTTP in local development. The deployment supplies
the public invite URL and MAX deep-link template; there are no invented default
domains. Keep the key/version stable for retained invites/replays. Multi-key
rotation is not implemented; unknown versions fail closed. Keys, tokens, intents
and request bodies are never logged by this package.

`cmd/server` mounts the route behind the existing `auth.Service.Middleware` and
injects `behavior.Recorder`. This adapter writes only through the supplied DBTX;
it neither starts another transaction nor opens its own pool. Tests exercise
the assembled server router and the production recorder against PostgreSQL.

The server requires `INVITE_ENCRYPTION_KEY` (standard base64 encoding of 32
bytes), `INVITE_URL_TEMPLATE` and `MAX_DEEP_LINK_TEMPLATE`. The positive
`INVITE_ENCRYPTION_KEY_VERSION` defaults to 1. Missing/invalid credentials fail
server startup without exposing their values. Migration/seed commands allow
these variables to be absent. Compose forwards them to the backend only.

## Transaction protocol

Use `Service.WithTx` and the shared `store.Pool.InTx` helper at READ COMMITTED.
Repositories receive a `pgx.Tx`; pass their `DBTX()` to the existing
`contracts.BehaviorRecorder`. Room changes and server behavior must commit or
roll back together. Do not retain repositories after the callback returns.

For future create/join operations, acquire locks in this order:

1. Lock the acting user's row with `FOR NO KEY UPDATE`. This serializes requests
   even when no active membership exists and remains compatible with foreign-key
   key-share locks. Reading only an absent membership cannot serialize create.
2. Discover the active membership, then lock all affected rooms in UUID order
   using `LockRooms`. Re-read membership/state after acquiring the room locks.
3. Lock invite and membership rows, then the active pool when required.

Invite hashes may be looked up before acquiring locks, but eligibility must be
checked again under the locks. Locking target and old rooms in arbitrary order,
or holding a membership lock while waiting for a room, can deadlock competing
joins and retirement. This refines the plan's illustrative lock sequence without
changing API semantics. A future join must use this same order throughout.

Room-local intent, pool, vote, match and cleanup transactions start with the room
lock and never subsequently acquire user locks. When multiple rooms are needed,
lock them together in UUID order before any child rows. Hold locks until commit.

## Persistence guarantees and caller responsibilities

- `InsertRoomMember` requires the parent room lock. Its SQL checks capacity and
  role against the room creator; serialized inserts enforce at most two members.
  Frozen partial unique indexes additionally enforce at most one creator and one
  active membership per user. Future create must insert room, creator and initial
  round state in one transaction to establish exactly one creator.
- Intent replacement and readiness updates belong to the same room transaction.
  Readiness and finished checks require two members and are scoped to the round.
- Pool metadata and bulk event rows commit together. Event order is zero-based;
  existing PK/unique constraints prevent duplicate events and positions. There is
  no pool-event update operation. Future pool orchestration must also validate
  candidate count, round/version, fingerprint and exclusion of earlier events.
- Vote inserts never update a conflict. Callers inspect the existing vote and
  affected-row count to implement retries and avoid duplicate behavior. Pool,
  event and membership foreign keys prevent cross-room votes.
- Match insertion uses the existing unique room constraint. Future match logic
  must verify mutual likes and update room state, memberships and privacy data in
  the same transaction. The insert primitive alone does not decide a match.
- Retirement and clearing exact coordinates are separate SQL primitives to be
  invoked together under the room lock on the terminal paths defined by the plan.
- Public participant queries project only public fields. Private persistence
  models are never HTTP response DTOs. No logging of intents or invite data is
  added by this foundation.

The transition graph does not check readiness, expiry, authorization, available
candidates or the three-round limit. These are mandatory orchestration guards
in their respective later phases. Frozen migrations, OpenAPI and shared
contracts remain unchanged.
