// Package maxwelcome handles MAX bot_started webhooks and sends a welcome message.
package maxwelcome

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	webhookPath = "/api/v1/max/webhook"
	apiBaseURL  = "https://platform-api2.max.ru"
	welcomeText = "Привет! Это „Вместе“ — здесь можно находить события и выбирать, куда пойти с друзьями. Откройте приложение, чтобы начать."
)

// Handler receives bot_started updates and delivers the greeting through the MAX API.
type Handler struct {
	client        *http.Client
	token         string
	appURL        string
	webhookSecret string
	baseURL       string
}

type update struct {
	Type string `json:"update_type"`
	User struct {
		ID int64 `json:"user_id"`
	} `json:"user"`
}

// New returns a disabled handler when no webhook secret is configured.
// A nil handler means that no public webhook route should be registered.
func New(token, appURL, webhookSecret string) (*Handler, error) {
	if strings.TrimSpace(webhookSecret) == "" {
		return nil, nil
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("MAX_BOT_TOKEN is required for the welcome webhook")
	}
	u, err := url.Parse(strings.TrimSpace(appURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("MAX_APP_URL must be an absolute https URL when the welcome webhook is enabled")
	}
	return &Handler{
		client:        &http.Client{Timeout: 10 * time.Second},
		token:         strings.TrimSpace(token),
		appURL:        u.String(),
		webhookSecret: webhookSecret,
		baseURL:       apiBaseURL,
	}, nil
}

// RegisterRoutes registers the unauthenticated MAX callback endpoint.
func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post(webhookPath, h.serveHTTP)
}

func (h *Handler) serveHTTP(w http.ResponseWriter, r *http.Request) {
	provided := r.Header.Get("X-Max-Bot-Api-Secret")
	if len(provided) != len(h.webhookSecret) || subtle.ConstantTimeCompare([]byte(provided), []byte(h.webhookSecret)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	defer r.Body.Close()
	var event update
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		http.Error(w, "invalid update", http.StatusBadRequest)
		return
	}
	if event.Type != "bot_started" {
		w.WriteHeader(http.StatusOK)
		return
	}
	if event.User.ID <= 0 {
		http.Error(w, "invalid bot_started update", http.StatusBadRequest)
		return
	}
	if err := h.sendWelcome(r, event.User.ID); err != nil {
		http.Error(w, "message delivery failed", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (h *Handler) sendWelcome(r *http.Request, userID int64) error {
	body, err := json.Marshal(map[string]any{
		"text": welcomeText,
		"attachments": []any{map[string]any{
			"type": "inline_keyboard",
			"payload": map[string]any{
				"buttons": [][]any{{map[string]any{
					"type": "open_app", "text": "Открыть приложение", "web_app": h.appURL,
				}}},
			},
		}},
	})
	if err != nil {
		return err
	}
	target := h.baseURL + "/messages?user_id=" + strconv.FormatInt(userID, 10)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", h.token)
	req.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("MAX API returned status %d", response.StatusCode)
	}
	return nil
}
