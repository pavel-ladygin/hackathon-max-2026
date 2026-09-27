package dailynotifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSendUsesMAXOpenAppButton(t *testing.T) {
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.Query().Get("user_id") != "1234" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		if r.Header.Get("Authorization") != "token" {
			t.Errorf("Authorization header missing")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode body: %v", err)
			return nil, err
		}
		if body["text"] == "" {
			t.Errorf("message text is empty")
		}
		attachments := body["attachments"].([]any)
		attachment := attachments[0].(map[string]any)
		if attachment["type"] != "inline_keyboard" {
			t.Errorf("attachment type = %v", attachment["type"])
		}
		buttons := attachment["payload"].(map[string]any)["buttons"].([]any)
		button := buttons[0].([]any)[0].(map[string]any)
		if button["type"] != "open_app" || button["web_app"] != "https://max.ru/example_bot" {
			t.Errorf("unexpected mini-app button: %#v", button)
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
	})
	appURL, _ := url.Parse("https://max.ru/example_bot")
	sender := &Sender{
		Client: &http.Client{Transport: transport}, Token: "token", AppURL: appURL.String(), BaseURL: "https://max.test",
		Now: func() time.Time { return time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC) },
	}
	if err := sender.send(context.Background(), recipient{MAXID: 1234, Sequence: 1}); err != nil {
		t.Fatalf("send: %v", err)
	}
}

type fakeDeliveryStore struct {
	mu      sync.Mutex
	users   []recipient
	consent map[string]bool
	status  map[string]string
	onClaim func(uuid.UUID)
}

func newFakeStore(users ...recipient) *fakeDeliveryStore {
	consent := make(map[string]bool)
	for _, user := range users {
		consent[user.ID.String()] = true
	}
	return &fakeDeliveryStore{users: users, consent: consent, status: make(map[string]string)}
}

func (f *fakeDeliveryStore) key(day string, id uuid.UUID) string {
	return fmt.Sprintf("%s/%s", day, id)
}
func (f *fakeDeliveryStore) OptedInUsers(context.Context) ([]recipient, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	users := make([]recipient, 0, len(f.users))
	for _, user := range f.users {
		if f.consent[user.ID.String()] {
			users = append(users, user)
		}
	}
	return users, nil
}
func (f *fakeDeliveryStore) Claim(_ context.Context, day string, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	key := f.key(day, id)
	if f.status[key] != "" && f.status[key] != "failed" {
		f.mu.Unlock()
		return false, nil
	}
	f.status[key] = "sending"
	callback := f.onClaim
	f.mu.Unlock()
	if callback != nil {
		callback(id)
	}
	return true, nil
}
func (f *fakeDeliveryStore) OptedIn(_ context.Context, id uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.consent[id.String()], nil
}
func (f *fakeDeliveryStore) Cancel(_ context.Context, day string, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[f.key(day, id)] = "rejected"
	return nil
}
func (f *fakeDeliveryStore) Finish(_ context.Context, day string, id uuid.UUID, status string, _ error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[f.key(day, id)] = status
	return nil
}
func (f *fakeDeliveryStore) MarkSent(_ context.Context, day string, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status[f.key(day, id)] = "sent"
	return nil
}

func testSender(store deliveryStore, transport roundTripFunc) *Sender {
	return &Sender{Store: store, Client: &http.Client{Transport: transport}, Token: "token", AppURL: "https://max.ru/example_bot", BaseURL: "https://max.test", Now: func() time.Time { return time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC) }, Sleep: func(context.Context, time.Duration) error { return nil }}
}

func okResponse() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}
}

func TestRunOnceOnlyTargetsOptedInAndRechecksConsent(t *testing.T) {
	user := recipient{ID: uuid.New(), MAXID: 50, Sequence: 1}
	optedOut := recipient{ID: uuid.New(), MAXID: 51, Sequence: 2}
	other := recipient{ID: uuid.New(), MAXID: 52, Sequence: 3}
	store := newFakeStore(user, optedOut, other)
	store.consent[optedOut.ID.String()] = false
	store.onClaim = func(id uuid.UUID) {
		if id != user.ID {
			return
		}
		store.mu.Lock()
		store.consent[id.String()] = false
		store.mu.Unlock()
	}
	requests := 0
	sender := testSender(store, func(*http.Request) (*http.Response, error) { requests++; return okResponse(), nil })
	if err := sender.RunOnce(context.Background(), time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if requests != 1 || store.status[store.key("2026-09-27", user.ID)] != "rejected" || store.status[store.key("2026-09-27", other.ID)] != "sent" {
		t.Fatalf("requests=%d rechecked=%q opted-out=%q other=%q", requests, store.status[store.key("2026-09-27", user.ID)], store.status[store.key("2026-09-27", optedOut.ID)], store.status[store.key("2026-09-27", other.ID)])
	}
}

func TestRunOnceLedgerPreventsRepeatAfterRestart(t *testing.T) {
	user := recipient{ID: uuid.New(), MAXID: 50, Sequence: 1}
	store := newFakeStore(user)
	requests := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { requests++; return okResponse(), nil })
	day := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	for range 2 {
		if err := testSender(store, transport).RunOnce(context.Background(), day); err != nil {
			t.Fatal(err)
		}
	}
	if requests != 1 {
		t.Fatalf("requests across restart = %d, want 1", requests)
	}
}

func TestRunOnceRetriesConfirmed429(t *testing.T) {
	user := recipient{ID: uuid.New(), MAXID: 50, Sequence: 1}
	store := newFakeStore(user)
	attempts := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{"Retry-After": []string{"0"}}, Body: io.NopCloser(strings.NewReader("limited"))}, nil
		}
		return okResponse(), nil
	})
	if err := testSender(store, transport).RunOnce(context.Background(), time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if attempts != 2 || store.status[store.key("2026-09-27", user.ID)] != "sent" {
		t.Fatalf("attempts=%d status=%q", attempts, store.status[store.key("2026-09-27", user.ID)])
	}
}

func TestAmbiguousFailureIsNotRetriedAfterRestart(t *testing.T) {
	user := recipient{ID: uuid.New(), MAXID: 50, Sequence: 1}
	store := newFakeStore(user)
	attempts := 0
	transport := roundTripFunc(func(*http.Request) (*http.Response, error) { attempts++; return nil, errors.New("connection reset") })
	day := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	for range 2 {
		_ = testSender(store, transport).RunOnce(context.Background(), day)
	}
	if attempts != 1 || store.status[store.key("2026-09-27", user.ID)] != "unknown" {
		t.Fatalf("attempts=%d status=%q", attempts, store.status[store.key("2026-09-27", user.ID)])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRetryAfterParsesSecondsAndDates(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("4", now); got != 4*time.Second {
		t.Fatalf("seconds Retry-After = %s", got)
	}
	if got := parseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); got != 5*time.Second {
		t.Fatalf("date Retry-After = %s", got)
	}
}

func TestNewRequiresPublicHTTPSMiniAppURL(t *testing.T) {
	if _, err := New(nil, "token", "http://max.ru/example_bot"); err == nil {
		t.Fatal("New accepted a non-HTTPS app URL")
	}
	if _, err := New(nil, "token", "https://max.ru/example_bot"); err != nil {
		t.Fatalf("New rejected a valid HTTPS app URL: %v", err)
	}
}

func TestNextNoon(t *testing.T) {
	before := time.Date(2026, 9, 27, 8, 59, 0, 0, time.UTC) // 11:59 Moscow
	if got := nextNoon(before); !got.Equal(time.Date(2026, 9, 27, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("next noon = %s", got)
	}
	after := time.Date(2026, 9, 27, 9, 1, 0, 0, time.UTC)
	if got := nextNoon(after); !strings.HasPrefix(got.In(moscowLocation()).Format(time.RFC3339), "2026-09-28T12:00:00") {
		t.Fatalf("next day noon = %s", got)
	}
}
