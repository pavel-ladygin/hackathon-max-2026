package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

type fakeAuthRepo struct {
	user            platform.User
	bootstrapHashes [][]byte
	byHash          map[[32]byte]platform.GetAuthSessionRow
	bootstrapErr    error
	sessionErr      error
}

func validOpaqueToken() string { return base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }

func (f *fakeAuthRepo) bootstrap(_ context.Context, _ identity, hash []byte, _ time.Time) (platform.User, error) {
	if f.bootstrapErr != nil {
		return platform.User{}, f.bootstrapErr
	}
	f.bootstrapHashes = append(f.bootstrapHashes, append([]byte(nil), hash...))
	return f.user, nil
}
func (f *fakeAuthRepo) session(_ context.Context, hash []byte) (platform.GetAuthSessionRow, error) {
	if f.sessionErr != nil {
		return platform.GetAuthSessionRow{}, f.sessionErr
	}
	row, ok := f.byHash[[32]byte(hash)]
	if !ok {
		return platform.GetAuthSessionRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func testService(t *testing.T, repo *fakeAuthRepo, now time.Time) *Service {
	t.Helper()
	validator, err := newMAXValidator("secret", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &Service{validator: validator, repo: repo, now: func() time.Time { return now }}
}

func TestServiceBootstrapPersistsOnlyTokenHashAndCreatesDistinctSessions(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo := &fakeAuthRepo{user: platform.User{ID: uuid.New(), DisplayName: "Ada", Locale: "en", OnboardingState: "new"}}
	s := testService(t, repo, now)
	values := validValues(now)
	values["auth_date"] = "1700000000"
	raw := signedInitData(t, "secret", values)
	one, err := s.bootstrap(context.Background(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.bootstrap(context.Background(), raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if one.token == two.token || len(repo.bootstrapHashes) != 2 {
		t.Fatalf("sessions are not distinct")
	}
	for i, token := range []string{one.token, two.token} {
		want := sha256.Sum256([]byte(token))
		if string(repo.bootstrapHashes[i]) != string(want[:]) {
			t.Fatalf("session %d persisted token instead of SHA-256 hash", i)
		}
		if len(repo.bootstrapHashes[i]) != sha256.Size {
			t.Fatalf("hash length = %d", len(repo.bootstrapHashes[i]))
		}
	}
}

func TestServiceAuthenticateRejectsInvalidExpiredAndRevokedTokens(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	userID := uuid.New()
	valid := validOpaqueToken()
	for name, row := range map[string]platform.GetAuthSessionRow{
		"expired":  {UserID: userID, ExpiresAt: pgtype.Timestamptz{Time: now, Valid: true}},
		"revoked":  {UserID: userID, ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}, RevokedAt: pgtype.Timestamptz{Time: now, Valid: true}},
		"nil user": {UserID: uuid.Nil, ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true}},
	} {
		t.Run(name, func(t *testing.T) {
			h := sha256.Sum256([]byte(valid))
			repo := &fakeAuthRepo{byHash: map[[32]byte]platform.GetAuthSessionRow{h: row}}
			_, err := testService(t, repo, now).Authenticate(context.Background(), valid)
			if name == "expired" {
				if !errors.Is(err, ErrTokenExpired) {
					t.Fatalf("got %v", err)
				}
			} else if !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("got %v", err)
			}
		})
	}
	for _, token := range []string{"short", "!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!"} {
		if _, err := testService(t, &fakeAuthRepo{}, now).Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("invalid token %q: %v", token, err)
		}
	}
}

func TestServiceAuthenticateReturnsPrincipalForValidSessionAndPropagatesRepositoryFailure(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	token := validOpaqueToken()
	userID := uuid.New()
	h := sha256.Sum256([]byte(token))
	repo := &fakeAuthRepo{byHash: map[[32]byte]platform.GetAuthSessionRow{h: {UserID: userID, ExpiresAt: pgtype.Timestamptz{Time: now.Add(time.Second), Valid: true}}}}
	principal, err := testService(t, repo, now).Authenticate(context.Background(), token)
	if err != nil || principal.UserID != userID {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	want := errors.New("repository unavailable")
	repo.sessionErr = want
	if _, err := testService(t, repo, now).Authenticate(context.Background(), token); !errors.Is(err, want) {
		t.Fatalf("got %v, want repository error", err)
	}
}
