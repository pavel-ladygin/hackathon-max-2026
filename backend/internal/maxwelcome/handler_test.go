package maxwelcome

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func testHandler(t *testing.T, api http.Handler) http.Handler {
	t.Helper()
	h, err := New("test-token", "https://max.ru/test_bot", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	h.client = apiClient(api)
	h.baseURL = "https://api.test"
	r := chi.NewRouter()
	h.RegisterRoutes(r)
	return r
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func apiClient(handler http.Handler) *http.Client {
	return &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		return res.Result(), nil
	})}
}

func TestWebhookSendsWelcomeOnBotStarted(t *testing.T) {
	var sent bool
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
		if r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.Query().Get("user_id") != "42" {
			t.Errorf("request = %s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "test-token" {
			t.Errorf("authorization header = %q", r.Header.Get("Authorization"))
		}
		var payload struct {
			Text        string `json:"text"`
			Attachments []struct {
				Type    string `json:"type"`
				Payload struct {
					Buttons [][]struct {
						Type   string `json:"type"`
						Text   string `json:"text"`
						WebApp string `json:"web_app"`
					} `json:"buttons"`
				} `json:"payload"`
			} `json:"attachments"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Text != welcomeText || len(payload.Attachments) != 1 || payload.Attachments[0].Type != "inline_keyboard" {
			t.Errorf("unexpected message payload: %+v", payload)
		}
		button := payload.Attachments[0].Payload.Buttons[0][0]
		if button.Type != "open_app" || button.Text != "Открыть приложение" || button.WebApp != "https://max.ru/test_bot" {
			t.Errorf("unexpected button: %+v", button)
		}
		w.WriteHeader(http.StatusOK)
	})
	r := testHandler(t, api)
	request := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(`{"update_type":"bot_started","user":{"user_id":42}}`))
	request.Header.Set("X-Max-Bot-Api-Secret", "test-secret")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !sent {
		t.Fatalf("status=%d sent=%v body=%s", response.Code, sent, response.Body.String())
	}
}

func TestWebhookIgnoresOtherUpdates(t *testing.T) {
	sent := false
	r := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(`{"update_type":"message_created","user":{"user_id":42}}`))
	request.Header.Set("X-Max-Bot-Api-Secret", "test-secret")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusOK || sent {
		t.Fatalf("status=%d sent=%v; want 200 and no message", response.Code, sent)
	}
}

func TestWebhookRejectsMissingOrInvalidSecretWithoutSending(t *testing.T) {
	sent := false
	r := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
		w.WriteHeader(http.StatusOK)
	}))
	for _, secret := range []string{"", "wrong-secret"} {
		request := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(`{"update_type":"bot_started","user":{"user_id":42}}`))
		request.Header.Set("X-Max-Bot-Api-Secret", secret)
		response := httptest.NewRecorder()
		r.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("secret %q returned %d; want 401", secret, response.Code)
		}
	}
	if sent {
		t.Fatal("message sent for unauthenticated webhook")
	}
}

func TestNewDisablesWebhookWithoutSecret(t *testing.T) {
	h, err := New("test-token", "https://max.ru/test_bot", " ")
	if err != nil || h != nil {
		t.Fatalf("New() = (%v, %v), want (nil, nil)", h, err)
	}
}

func TestWebhookReturnsErrorWhenMAXAPIFails(t *testing.T) {
	r := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "unavailable")
	}))
	request := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(`{"update_type":"bot_started","user":{"user_id":42}}`))
	request.Header.Set("X-Max-Bot-Api-Secret", "test-secret")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s; want 502", response.Code, response.Body.String())
	}
}

func TestWebhookRejectsInvalidBotStartedUpdateWithoutSending(t *testing.T) {
	sent := false
	r := testHandler(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = true
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodPost, webhookPath, strings.NewReader(`{"update_type":"bot_started","user":{"user_id":0}}`))
	request.Header.Set("X-Max-Bot-Api-Secret", "test-secret")
	response := httptest.NewRecorder()
	r.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || sent {
		t.Fatalf("status=%d sent=%v; want 400 and no message", response.Code, sent)
	}
}
