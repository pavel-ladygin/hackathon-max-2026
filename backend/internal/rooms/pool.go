package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

var errPoolBuilderUnavailable = errors.New("room pool builder is not configured")

// buildRoomPool runs after the room row has been locked and both round states
// are ready. All writes remain on the caller's transaction and roll back when
// the builder fails or any persistence operation fails.
func (s *Service) buildRoomPool(ctx context.Context, repo *Repository, room roomsql.Room) error {
	if s.builder == nil {
		return errPoolBuilderUnavailable
	}
	states, err := repo.Queries.GetRoomRoundStates(ctx, roomsql.GetRoomRoundStatesParams{RoomID: room.ID, RoundNo: room.RoundNo})
	if err != nil {
		return err
	}
	activeStates := states[:0]
	for _, state := range states {
		member, err := repo.Queries.GetRoomMembership(ctx, roomsql.GetRoomMembershipParams{RoomID: room.ID, UserID: state.UserID})
		if err != nil {
			return err
		}
		if member.IsActive {
			activeStates = append(activeStates, state)
		}
	}
	if len(activeStates) != 2 {
		return fmt.Errorf("room %s has %d active members, want 2", room.ID, len(activeStates))
	}
	first, err := repo.Queries.GetRoomIntent(ctx, roomsql.GetRoomIntentParams{RoomID: room.ID, UserID: activeStates[0].UserID, RoundNo: room.RoundNo})
	if err != nil {
		return err
	}
	second, err := repo.Queries.GetRoomIntent(ctx, roomsql.GetRoomIntentParams{RoomID: room.ID, UserID: activeStates[1].UserID, RoundNo: room.RoundNo})
	if err != nil {
		return err
	}
	previous, err := repo.Queries.GetOldRoomPoolEventIDs(ctx, room.ID)
	if err != nil {
		return err
	}
	input := contracts.BuildInput{
		RoomID: room.ID, CityID: room.CityID, RoundNo: room.RoundNo,
		PoolVersion: room.ActivePoolVersion + 1, PreviousEventIDs: previous,
		FirstIntent: participantIntent(first), SecondIntent: participantIntent(second),
	}
	if _, err := repo.Queries.UpdateRoomState(ctx, roomsql.UpdateRoomStateParams{ID: room.ID, State: string(RoomStateRanking)}); err != nil {
		return err
	}
	result, err := s.builder.Build(store.WithDBTX(ctx, repo.DBTX()), input)
	if err != nil {
		return err
	}
	count := len(result.Candidates)
	isSmall := result.IsSmall
	if count >= 1 && count <= 2 {
		isSmall = true
	}
	diagnostics, err := json.Marshal(result.Diagnostics)
	if err != nil {
		return err
	}
	poolState := "ready"
	if count == 0 {
		poolState = "exhausted"
	}
	poolID := uuid.New()
	if _, err := repo.Queries.InsertRoomPool(ctx, roomsql.InsertRoomPoolParams{
		ID: poolID, RoomID: room.ID, Version: input.PoolVersion, RoundNo: room.RoundNo,
		RankerVersion: result.RankerVersion, InputFingerprint: fmt.Sprintf("round:%d:%s", room.RoundNo, result.InputFingerprint),
		State: poolState, CandidateCount: int32(count), IsSmall: isSmall, Diagnostics: diagnostics,
	}); err != nil {
		return err
	}
	events := make([]roomsql.InsertRoomPoolEventsParams, 0, count)
	for i, candidate := range result.Candidates {
		explanation, err := json.Marshal(candidate.Explanation)
		if err != nil {
			return err
		}
		features, err := json.Marshal(candidate.FeatureSnapshot)
		if err != nil {
			return err
		}
		events = append(events, roomsql.InsertRoomPoolEventsParams{
			PoolID: poolID, EventID: candidate.EventID, Position: int32(i),
			GroupScore: candidate.Score.GroupScore, ParticipantScoreMin: candidate.Score.ParticipantScoreMin,
			ParticipantScoreMean: candidate.Score.ParticipantScoreMean,
			Explanation:          explanation, FeatureSnapshot: features,
		})
	}
	if count > 0 {
		if _, err := repo.Queries.InsertRoomPoolEvents(ctx, events); err != nil {
			return err
		}
		if _, err := repo.Queries.ActivateRoomPool(ctx, roomsql.ActivateRoomPoolParams{ID: room.ID, ActivePoolVersion: input.PoolVersion, State: string(RoomStateVoting)}); err != nil {
			return err
		}
	} else {
		if _, err := repo.Queries.ActivateRoomPool(ctx, roomsql.ActivateRoomPoolParams{ID: room.ID, ActivePoolVersion: input.PoolVersion, State: string(RoomStateExhausted)}); err != nil {
			return err
		}
		// A zero-candidate pool exhausts both participants immediately.  Mark
		// both round states in the caller's transaction so the snapshot exposed
		// after this build is consistent for either participant.
		for _, state := range activeStates {
			if _, err := repo.Queries.MarkPoolFinished(ctx, roomsql.MarkPoolFinishedParams{
				RoomID: room.ID, UserID: state.UserID, RoundNo: room.RoundNo,
			}); err != nil {
				return err
			}
		}
		// A zero-candidate pool exhausts both participants immediately.  The
		// final round has no restart path, so apply the same terminal cleanup as
		// the voting exhaustion path.
		if err := s.finalizeExhaustedRoom(ctx, repo, room); err != nil {
			return err
		}
		// Do not reset round completion markers here: zero candidates means the
		// pool is already exhausted for both members.
		return nil
	}
	if _, err := repo.Queries.ResetRoundPoolFinished(ctx, roomsql.ResetRoundPoolFinishedParams{RoomID: room.ID, RoundNo: room.RoundNo}); err != nil {
		return err
	}
	return nil
}

func participantIntent(in roomsql.RoomIntent) contracts.ParticipantIntent {
	out := contracts.ParticipantIntent{UserID: in.UserID, Version: in.Version, BudgetMaxMinor: in.BudgetMaxMinor}
	for _, date := range in.DateOptions {
		out.Dates = append(out.Dates, date.Time.Format("2006-01-02"))
	}
	out.DayTypes = append([]string(nil), in.DayTypes...)
	out.TimeSlots = append([]string(nil), in.TimeSlots...)
	out.CategorySlugs = append([]string(nil), in.CategorySlugs...)
	out.ExclusionSlugs = append([]string(nil), in.ExclusionSlugs...)
	// Room recommendations currently do not use participant geolocation. Keep
	// the persisted fields for future use, but do not let stale values narrow
	// pools for either existing rooms or newly submitted intents.
	if in.FreeText.Valid {
		v := in.FreeText.String
		out.FreeText = &v
	}
	return out
}
