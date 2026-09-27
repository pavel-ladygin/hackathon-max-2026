package rooms

import "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"

// RoomState is the canonical HTTP contract state type. This foundation only
// describes legal edges; lifecycle code must still lock the room and validate
// its operation-specific preconditions before applying one.
type RoomState = openapi.RoomState

const (
	RoomStateCollectingIntents = openapi.RoomStateCollectingIntents
	RoomStateRanking           = openapi.RoomStateRanking
	RoomStateVoting            = openapi.RoomStateVoting
	RoomStateMatched           = openapi.RoomStateMatched
	RoomStateExhausted         = openapi.RoomStateExhausted
	RoomStateClosed            = openapi.RoomStateClosed
)

// CanTransition reports whether a distinct direct lifecycle transition is
// permitted. Matched is terminal; restart logic owns exhausted -> collecting.
func CanTransition(from, to RoomState) bool {
	if to == RoomStateClosed {
		return from == RoomStateCollectingIntents || from == RoomStateRanking || from == RoomStateVoting || from == RoomStateMatched || from == RoomStateExhausted
	}
	switch from {
	case RoomStateCollectingIntents:
		return to == RoomStateRanking
	case RoomStateRanking:
		return to == RoomStateVoting || to == RoomStateExhausted
	case RoomStateVoting:
		return to == RoomStateMatched || to == RoomStateExhausted
	case RoomStateExhausted:
		return to == RoomStateCollectingIntents
	default:
		return false
	}
}
