package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/contracts"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

const SessionTTL = 24 * time.Hour

type repository interface {
	bootstrap(context.Context, identity, []byte, time.Time) (platform.User, error)
	session(context.Context, []byte) (platform.GetAuthSessionRow, error)
}

// Service validates MAX credentials and resolves opaque sessions to internal principals.
type Service struct {
	validator maxValidator
	repo      repository
	now       func() time.Time
}

func NewService(db *store.Pool, botToken string, maxAge time.Duration) (*Service, error) {
	validator, err := newMAXValidator(botToken, maxAge)
	if err != nil {
		return nil, err
	}
	return &Service{validator: validator, repo: postgresRepository{db}, now: time.Now}, nil
}

type bootstrapResult struct {
	token string
	user  platform.User
}

func (s *Service) bootstrap(ctx context.Context, raw string, hint *string) (bootstrapResult, error) {
	claims, err := s.validator.validate(raw, s.now())
	if err != nil {
		return bootstrapResult{}, err
	}
	if hint != nil && *hint != claims.startParam {
		return bootstrapResult{}, errInvalidRequest
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return bootstrapResult{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random[:])
	hash := sha256.Sum256([]byte(token))
	user, err := s.repo.bootstrap(ctx, claims, hash[:], s.now().Add(SessionTTL))
	if err != nil {
		return bootstrapResult{}, err
	}
	return bootstrapResult{token: token, user: user}, nil
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

func (r postgresRepository) bootstrap(ctx context.Context, claims identity, hash []byte, expires time.Time) (platform.User, error) {
	var user platform.User
	err := r.db.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		q := platform.New(tx)
		var err error
		user, err = q.UpsertMAXUser(ctx, platform.UpsertMAXUserParams{
			ID: uuid.New(), MaxUserID: claims.maxUserID, DisplayName: claims.displayName,
			AvatarUrl: pgtype.Text{String: claims.avatarURL, Valid: claims.avatarURL != ""}, Locale: claims.locale,
		})
		if err != nil {
			return err
		}
		return q.CreateAuthSession(ctx, platform.CreateAuthSessionParams{ID: uuid.New(), UserID: user.ID, TokenHash: hash, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
	})
	return user, err
}

func (r postgresRepository) session(ctx context.Context, hash []byte) (platform.GetAuthSessionRow, error) {
	return platform.New(r.db.Pool).GetAuthSession(ctx, hash)
}
