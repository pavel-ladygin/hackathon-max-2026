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
	api "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/httpapi/openapi"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/preferences"
	platform "github.com/pavel-ladygin/hackathon-max-2026/backend/internal/store/platform/generated"
)

type fakeAuthRepo struct {
	user                      platform.User
	preferences               *preferences.Value
	dailyNotificationsEnabled bool
	bootstrapHashes           [][]byte
	byHash                    map[[32]byte]platform.GetAuthSessionRow
	bootstrapErr              error
	sessionErr                error
}

func validOpaqueToken() string { return base64.RawURLEncoding.EncodeToString(make([]byte, 32)) }

func (f *fakeAuthRepo) bootstrap(_ context.Context, _ identity, hash []byte, _ time.Time) (bootstrapProfile, error) {
	if f.bootstrapErr != nil {
		return bootstrapProfile{}, f.bootstrapErr
	}
	f.bootstrapHashes = append(f.bootstrapHashes, append([]byte(nil), hash...))
	return bootstrapProfile{user: f.user, preferences: f.preferences, dailyNotificationsEnabled: f.dailyNotificationsEnabled}, nil
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

type recordingInviteResolver struct{ tokens []string }

func (r *recordingInviteResolver) ResolveInviteContext(_ context.Context, _ uuid.UUID, token string) (*api.InviteContext, error) {
	r.tokens = append(r.tokens, token)
	return nil, nil
}

func TestBootstrapRoutesEventStartParamSeparatelyFromRoomInvite(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	resolver := &recordingInviteResolver{}
	for _, tc := range []struct {
		name         string
		startParam   string
		wantEventID  uuid.UUID
		wantResolver bool
	}{
		{name: "shared event", startParam: "event_22222222-2222-4222-8222-222222222222", wantEventID: uuid.MustParse("22222222-2222-4222-8222-222222222222")},
		{name: "room invite", startParam: "opaque-room-invite", wantResolver: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAuthRepo{user: platform.User{ID: uuid.New(), DisplayName: "Ada", Locale: "en", OnboardingState: "new"}}
			s := testService(t, repo, now)
			s.SetInviteContextResolver(resolver)
			values := validValues(now)
			values["auth_date"] = "1700000000"
			values["start_param"] = tc.startParam
			before := len(resolver.tokens)
			result, err := s.bootstrap(context.Background(), signedInitData(t, "secret", values), nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantEventID != uuid.Nil {
				if result.sharedEventID == nil || *result.sharedEventID != tc.wantEventID || result.invite != nil {
					t.Fatalf("event bootstrap result=%+v", result)
				}
			} else if result.sharedEventID != nil {
				t.Fatalf("unexpected shared event id %v", result.sharedEventID)
			}
			if got := len(resolver.tokens) - before; got != btoi(tc.wantResolver) {
				t.Fatalf("resolver calls=%d, wantResolver=%v", got, tc.wantResolver)
			}
			if tc.wantResolver && resolver.tokens[len(resolver.tokens)-1] != tc.startParam {
				t.Fatalf("resolver token=%q, want %q", resolver.tokens[len(resolver.tokens)-1], tc.startParam)
			}
		})
	}
}

func TestBootstrapRejectsMalformedEventStartParamBeforeCreatingSession(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	repo := &fakeAuthRepo{user: platform.User{ID: uuid.New(), DisplayName: "Ada", Locale: "en", OnboardingState: "new"}}
	s := testService(t, repo, now)
	values := validValues(now)
	values["auth_date"] = "1700000000"
	values["start_param"] = "event_not-a-uuid"
	if _, err := s.bootstrap(context.Background(), signedInitData(t, "secret", values), nil); !errors.Is(err, errInvalidRequest) {
		t.Fatalf("error=%v, want invalid request", err)
	}
	if len(repo.bootstrapHashes) != 0 {
		t.Fatalf("malformed event parameter created %d sessions", len(repo.bootstrapHashes))
	}
}

func btoi(value bool) int {
	if value {
		return 1
	}
	return 0
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
