# Recommendation pool

Production constructs `NewPoolBuilderWithBehavior` with the catalog, permanent
preferences, and provider-neutral behavioral loaders. The tie-break secret must
contain at least 32 bytes; the builder keeps a private copy. Backend B passes the
frozen `contracts.BuildInput`, owns pool persistence and room lifecycle, while this
package reads one coherent catalog snapshot and Backend A personalization data.

Both participants' A3 hard constraints apply before ranking. Event start time uses
the city timezone. Eligibility requires a published event, an available ticket,
and a known price within the joint budget. Requested metro proximity with no city
metro data fails closed. Earlier pool events are excluded before ranking.

A4 ranks all eligible events as `scoring-diversity-v3-behavior`. The group score combines
the lower participant score (65%) and their mean (35%). Participant scores use
category affinity, current category fit, time quality, budget headroom, distance
quality, novelty, popularity, and a small behavioral category adjustment. Permanent
preferences contribute less than current intent; smoothed immutable room votes add
at most 0.06 and never affect eligibility. Cold start is neutral. Candidate snapshots
contain only aggregate component means and safe, fixed explanations.

Equal scores use an HMAC-SHA256 digest over room ID, pool version, and event ID.
The digest is compared as raw bytes, making catalog iteration order irrelevant.
The input fingerprint includes normalized category selections, participant order,
effective hard inputs, profile and behavioral digests, ranker version, and an
opaque key identifier; it never includes raw preferences, votes, free text, or
submission time.

The ordered list is diversified deterministically up to 24 candidates (with 20 as
the presentation target). It brings up to four distinct non-empty primary
categories forward where possible, then observes a three-item primary-category
streak cap and a two-item venue streak cap, relaxing those caps only when needed
to retain the pool. Sparse pools are returned unchanged in size; one or two
candidates set `IsSmall`, and an empty pool has a generic safe diagnostic.

The separate vote-time `EventAvailability` adapter belongs to A5. This package
does not read or write rooms, pools, votes, or matches.
