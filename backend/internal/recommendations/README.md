# Pool hard eligibility

Construct `NewPoolBuilder(catalog.NewRepository(db))` and pass the frozen
`contracts.BuildInput`. The builder reads one coherent catalog snapshot; Backend B
supplies private intents and all previous event IDs and persists the result.

Both participants' hard constraints apply. Event start time is interpreted in the
city timezone. Intent date freshness is validated by the caller; building does
not consult the wall clock. Eligibility requires `published`, available tickets
and a known price within the joint budget. Requested metro proximity with no
city metro data fails closed.

The first 20 eligible events are ordered by start time, then event ID. One or two
candidates set `IsSmall`; zero returns a generic safe exhaustion reason. Categories
and free text do not constrain eligibility. Scores remain zero and explanations
and feature snapshots remain empty. The version is `hard-filters-v1` and the
fingerprint hashes normalized hard inputs without exposing their private values.

Scoring, diversity, HMAC ranking and recommendation explanations belong to A4.
The separate vote-time `EventAvailability` adapter belongs to A5. This package
does not read or write rooms, pools, votes or matches.
