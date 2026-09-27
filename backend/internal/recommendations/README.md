# Recommendation pool

Production constructs `NewPoolBuilderWithBehavior` with the catalog, permanent
preferences, and provider-neutral behavioral loaders. The tie-break secret must
contain at least 32 bytes; the builder keeps a private copy. Backend B passes the
frozen `contracts.BuildInput`, owns pool persistence and room lifecycle, while this
package reads one coherent catalog snapshot and Backend A personalization data.

Both participants' A3 hard constraints apply before ranking. Event start time uses
the city timezone. Eligibility requires a published event and an available ticket.
Known prices must be within the joint budget; events with unknown prices remain
eligible but receive no budget-fit score. Requested metro proximity with no city
metro data fails closed. Earlier pool events are excluded before ranking.

A4 ranks all eligible events as `scoring-diversity-v6-behavioral`. The group score
combines the lower participant score (65%) and their mean (35%). Participant
scores use permanent category affinity, current category fit, budget headroom,
distance quality, and smoothed behavioral category affinity. The retained weights
are normalized to a 0..1 score. Date and time constraints affect eligibility;
they are not counted again as a ranking feature. Constant novelty and unavailable
popularity values are not scored or persisted. Behavioral affinity adds at most
0.06 before normalization and cold start is neutral. Candidate snapshots contain
the aggregate behavioral affinity alongside the other component means, with safe,
fixed explanations.

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
