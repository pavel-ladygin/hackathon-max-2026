# Backend B transaction foundation (B1)

B1 supplies transaction-bound repositories, sqlc primitives and the room state
transition graph. HTTP operations and lifecycle orchestration start in B2 and
later phases; the graph alone does not authorize a transition.

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
