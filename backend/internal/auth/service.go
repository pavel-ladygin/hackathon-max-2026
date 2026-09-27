package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const SessionTTL = 24 * time.Hour

type repository interface {
	bootstrap(context.Context, identity, []byte, time.Time) (bootstrapProfile, error)
	session(context.Context, []byte) (platform.GetAuthSessionRow, error)
}

type bootstrapProfile struct {
	user                      platform.User
	preferences               *preferences.Value
	dailyNotificationsEnabled bool
}

// Service validates MAX credentials and resolves opaque sessions to internal principals.
type Service struct {
	validator         maxValidator
	repo              repository
	now               func() time.Time
	trustedProxyCIDRs []net.IPNet
	inviteResolver    InviteContextResolver
}

type InviteContextResolver interface {
	ResolveInviteContext(context.Context, uuid.UUID, string) (*api.InviteContext, error)
}

func (s *Service) SetInviteContextResolver(resolver InviteContextResolver) {
	s.inviteResolver = resolver
}

func NewService(db *store.Pool, botToken string, maxAge time.Duration) (*Service, error) {
	return NewServiceWithTrustedProxyCIDRs(db, botToken, maxAge, nil)
}

// NewServiceWithTrustedProxyCIDRs configures the optional proxy networks used
// to safely derive the client IP from X-Forwarded-For. An empty list preserves
// the direct-connection behavior of NewService.
func NewServiceWithTrustedProxyCIDRs(db *store.Pool, botToken string, maxAge time.Duration, trustedProxyCIDRs []net.IPNet) (*Service, error) {
	validator, err := newMAXValidator(botToken, maxAge)
	if err != nil {
		return nil, err
	}
	return &Service{validator: validator, repo: postgresRepository{db}, now: time.Now, trustedProxyCIDRs: trustedProxyCIDRs}, nil
}

type bootstrapResult struct {
	token                     string
	user                      platform.User
	preferences               *preferences.Value
	dailyNotificationsEnabled bool
	invite                    *api.InviteContext
	sharedEventID             *uuid.UUID
}

func (s *Service) bootstrap(ctx context.Context, raw string, hint *string) (bootstrapResult, error) {
	claims, err := s.validator.validate(raw, s.now())
	if err != nil {
		return bootstrapResult{}, err
	}
	if hint != nil && *hint != claims.startParam {
		return bootstrapResult{}, errInvalidRequest
	}
	var sharedEventID *uuid.UUID
	if strings.HasPrefix(claims.startParam, "event_") {
		eventID, err := parseSharedEventStartParam(claims.startParam)
		if err != nil {
			return bootstrapResult{}, errInvalidRequest
		}
		sharedEventID = &eventID
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return bootstrapResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	profile, err := s.repo.bootstrap(ctx, claims, hash[:], s.now().Add(SessionTTL))
	if err != nil {
		return bootstrapResult{}, err
	}
	result := bootstrapResult{token: token, user: profile.user, preferences: profile.preferences, dailyNotificationsEnabled: profile.dailyNotificationsEnabled, sharedEventID: sharedEventID}
	if sharedEventID == nil && claims.startParam != "" && s.inviteResolver != nil {
		result.invite, err = s.inviteResolver.ResolveInviteContext(ctx, profile.user.ID, claims.startParam)
		if err != nil {
			return bootstrapResult{}, err
		}
	}
	return result, nil
}

func parseSharedEventStartParam(startParam string) (uuid.UUID, error) {
	const prefix = "event_"
	if !strings.HasPrefix(startParam, prefix) {
		return uuid.Nil, errInvalidRequest
	}
	value := strings.TrimPrefix(startParam, prefix)
	if len(value) != 36 {
		return uuid.Nil, errInvalidRequest
	}
	eventID, err := uuid.Parse(value)
	if err != nil {
		return uuid.Nil, errInvalidRequest
	}
	return eventID, nil
}

// Authenticate accepts only application tokens and never returns MAX identity.
func (s *Service) Authenticate(ctx context.Context, token string) (contracts.Principal, error) {
	if len(token) != 43 {
		return contracts.Principal{}, ErrUnauthenticated
	}
	random, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(random) != 32 {
		return contracts.Principal{}, ErrUnauthenticated
	}
	hash := sha256.Sum256([]byte(token))
	session, err := s.repo.session(ctx, hash[:])
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.Principal{}, ErrUnauthenticated
	}
	if err != nil {
		return contracts.Principal{}, err
	}
	if session.RevokedAt.Valid || session.UserID == uuid.Nil {
		return contracts.Principal{}, ErrUnauthenticated
	}
	if !session.ExpiresAt.Valid || !session.ExpiresAt.Time.After(s.now()) {
		return contracts.Principal{}, ErrTokenExpired
	}
	return contracts.Principal{UserID: session.UserID}, nil
}

type postgresRepository struct{ db *store.Pool }

func (r postgresRepository) bootstrap(ctx context.Context, claims identity, hash []byte, expires time.Time) (bootstrapProfile, error) {
	var profile bootstrapProfile
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		var err error
		profile.user, err = q.UpsertMAXUser(ctx, platform.UpsertMAXUserParams{
			ID: uuid.New(), MaxUserID: claims.maxUserID, DisplayName: claims.displayName,
			AvatarUrl: pgtype.Text{String: claims.avatarURL, Valid: claims.avatarURL != ""}, Locale: claims.locale,
		})
		if err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT daily_notifications_enabled FROM users WHERE id = $1`, profile.user.ID).Scan(&profile.dailyNotificationsEnabled); err != nil {
			return err
		}
		preferenceRow, err := q.GetUserPreferences(ctx, profile.user.ID)
		if err == nil {
			categories, err := q.ListUserPreferenceCategories(ctx, profile.user.ID)
			if err != nil {
				return err
			}
			profile.preferences = &preferences.Value{
				CityID: preferenceRow.CityID, InterestSlugs: categories, BudgetMaxMinor: int(preferenceRow.BudgetMaxMinor),
				UsualDayTypes: preferenceRow.UsualDayTypes, UsualTimeSlots: preferenceRow.UsualTimeSlots,
				Version: int(preferenceRow.Version), UpdatedAt: preferenceRow.UpdatedAt.Time,
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return q.CreateAuthSession(ctx, platform.CreateAuthSessionParams{ID: uuid.New(), UserID: profile.user.ID, TokenHash: hash, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
	})
	return profile, err
}

func (r postgresRepository) session(ctx context.Context, hash []byte) (platform.GetAuthSessionRow, error) {
	return platform.New(r.db.Pool).GetAuthSession(ctx, hash)
}
