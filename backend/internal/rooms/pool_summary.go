package rooms

import (
	"bytes"
	"encoding/json"
	"io"
	"unicode/utf8"

	"github.com/oapi-codegen/nullable"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	roomsql "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/rooms/generated"
)

// poolExhaustionReasons decodes only the public, canonical reason codes. Any
// malformed or unexpected diagnostics are omitted in full so database data
// cannot become an accidental API surface.
func poolExhaustionReasons(raw []byte) *[]struct {
	Code api.PoolSummaryExhaustionReasonsCode `json:"code"`
	Text string                               `json:"text"`
} {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var diagnostics contracts.ExhaustionDiagnostics
	if err := decoder.Decode(&diagnostics); err != nil {
		return nil
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil
	}
	reasons := make([]struct {
		Code api.PoolSummaryExhaustionReasonsCode `json:"code"`
		Text string                               `json:"text"`
	}, 0, len(diagnostics.Reasons))
	for _, reason := range diagnostics.Reasons {
		code := api.PoolSummaryExhaustionReasonsCode(reason.Code)
		if !code.Valid() || !utf8.ValidString(reason.Text) {
			return nil
		}
		reasons = append(reasons, struct {
			Code api.PoolSummaryExhaustionReasonsCode `json:"code"`
			Text string                               `json:"text"`
		}{Code: code, Text: reason.Text})
	}
	if len(reasons) == 0 {
		return nil
	}
	return &reasons
}

func poolSummary(pool roomsql.RoomPool, voted int, myPoolFinished, roomExhausted bool) api.PoolSummary {
	return api.PoolSummary{
		Version: int(pool.Version), RoundNo: int(pool.RoundNo), State: api.PoolSummaryState(pool.State),
		Total: int(pool.CandidateCount), IsSmall: pool.IsSmall, VotedByMe: voted,
		MyPoolFinished: myPoolFinished, RoomExhausted: roomExhausted,
		RetryAfterSeconds: nullable.NewNullNullable[int](), ExhaustionReasons: poolExhaustionReasons(pool.Diagnostics),
	}
}
