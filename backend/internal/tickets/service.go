// Package tickets provides the verified external ticket-link capability.
package tickets

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

var (
	ErrInvalid     = errors.New("invalid ticket click")
	ErrNotFound    = errors.New("event not found")
	ErrUnavailable = errors.New("ticket unavailable")
)

type hostRule struct {
	host       string
	subdomains bool
}

// Service validates an event's current ticket link before returning it.
type Service struct {
	db        *store.Pool
	recorder  contracts.BehaviorRecorder
	allowlist []hostRule
	now       func() time.Time
}

func NewService(db *store.Pool, recorder contracts.BehaviorRecorder, allowlist []string) (*Service, error) {
	if db == nil {
		return nil, errors.New("ticket service database is required")
	}
	if recorder == nil {
		return nil, errors.New("ticket service behavior recorder is required")
	}
	rules, err := parseAllowlist(allowlist)
	if err != nil {
		return nil, err
	}
	return &Service{db: db, recorder: recorder, allowlist: rules, now: time.Now}, nil
}

// Click returns a checked external URL and records the completed server action.
// Optional room context is attached only when the event is the room's match and
// the requesting user is a member; membership need not still be active.
func (s *Service) Click(ctx context.Context, userID, eventID uuid.UUID, roomIDs ...uuid.UUID) (string, error) {
	if userID == uuid.Nil || eventID == uuid.Nil {
		return "", ErrInvalid
	}
	var matchedRoomID *uuid.UUID
	if len(roomIDs) > 0 && roomIDs[0] != uuid.Nil {
		roomID := roomIDs[0]
		var memberHasMatch bool
		err := s.db.QueryRow(ctx, `SELECT EXISTS (
			SELECT 1
			FROM room_matches match
			JOIN room_members member ON member.room_id = match.room_id
			WHERE match.room_id = $1 AND match.event_id = $2 AND member.user_id = $3
		)`, roomID, eventID, userID).Scan(&memberHasMatch)
		if err != nil {
			slog.Default().Warn("ticket room context validation failed", "request_id", httpapi.RequestID(ctx), "error", err)
		} else if memberHasMatch {
			matchedRoomID = &roomID
		}
	}

	var externalURL string
	err := s.db.InTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, func(tx pgx.Tx) error {
		availability, err := platform.New(tx).GetEventAvailability(ctx, eventID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if availability.Status != "published" || !availability.StartsAt.Valid || !availability.StartsAt.Time.After(s.now()) || !availability.TicketAvailable || !availability.TicketUrl.Valid {
			return ErrUnavailable
		}
		if !s.isAllowedURL(availability.TicketUrl.String) {
			return ErrUnavailable
		}

		externalURL = availability.TicketUrl.String
		return s.recorder.Record(ctx, tx, contracts.ServerBehaviorEvent{
			ID: uuid.New(), UserID: userID, Type: "ticket_click", EventID: &eventID,
			RoomID:    matchedRoomID,
			RequestID: httpapi.RequestID(ctx), OccurredAt: s.now(),
			DeduplicationKey: "ticket/" + eventID.String() + "/" + httpapi.RequestID(ctx),
		})
	})
	if err != nil {
		return "", err
	}
	return externalURL, nil
}

func parseAllowlist(values []string) ([]hostRule, error) {
	rules := make([]hostRule, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		rule := hostRule{host: value}
		if strings.HasPrefix(value, "*.") {
			rule.subdomains = true
			rule.host = strings.TrimPrefix(value, "*.")
		}
		if rule.host == "" || strings.Contains(value, ":") || net.ParseIP(rule.host) != nil || !validDNSName(rule.host) {
			return nil, fmt.Errorf("TICKET_PROVIDER_ALLOWLIST is invalid")
		}
		key := fmt.Sprintf("%t:%s", rule.subdomains, rule.host)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		rules = append(rules, rule)
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("TICKET_PROVIDER_ALLOWLIST is required")
	}
	return rules, nil
}

func validDNSName(host string) bool {
	if len(host) > 253 || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character != '-' && (character < 'a' || character > 'z') && (character < '0' || character > '9') {
				return false
			}
		}
	}
	return true
}

func (s *Service) isAllowedURL(raw string) bool {
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Host == "" || parsed.User != nil || parsed.Port() != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == "" || net.ParseIP(host) != nil || !validDNSName(host) {
		return false
	}
	for _, rule := range s.allowlist {
		if (!rule.subdomains && host == rule.host) || (rule.subdomains && strings.HasSuffix(host, "."+rule.host)) {
			return true
		}
	}
	return false
}
