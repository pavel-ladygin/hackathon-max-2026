package rooms

import "testing"

func TestCanTransition(t *testing.T) {
	tests := []struct {
		from, to RoomState
		want     bool
	}{
		{RoomStateCollectingIntents, RoomStateRanking, true},
		{RoomStateRanking, RoomStateVoting, true},
		{RoomStateRanking, RoomStateExhausted, true},
		{RoomStateVoting, RoomStateMatched, true},
		{RoomStateVoting, RoomStateExhausted, true},
		{RoomStateExhausted, RoomStateCollectingIntents, true},
		{RoomStateMatched, RoomStateCollectingIntents, false},
		{RoomStateMatched, RoomStateMatched, false},
		{RoomStateVoting, RoomStateVoting, false},
		{RoomStateCollectingIntents, RoomStateMatched, false},
		{RoomStateExhausted, RoomStateVoting, false},
	}
	for _, tt := range tests {
		t.Run(string(tt.from)+"_to_"+string(tt.to), func(t *testing.T) {
			if got := CanTransition(tt.from, tt.to); got != tt.want {
				t.Fatalf("CanTransition(%q, %q) = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}
