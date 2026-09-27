// Package dailynotifications sends one opt-in daily prompt to MAX users.
package dailynotifications

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const maxAPIBaseURL = "https://platform-api2.max.ru"

var prompts = []string{
	"Привет! Может, сегодня выбраться куда-нибудь с друзьями?",
	"Есть повод написать друзьям и придумать, куда сходить вместе сегодня.",
	"Как насчёт небольшого приключения с друзьями? Загляни в приложение за идеями!",
	"Сегодня хороший день, чтобы увидеться с друзьями. Вы уже выбрали место?",
	"Позови друзей на прогулку, выставку или кофе — пусть день запомнится!",
}

type Sender struct {
	Store    deliveryStore
	Client   *http.Client
	Token    string
	AppURL   string
	BaseURL  string
	Now      func() time.Time
	Sleep    func(context.Context, time.Duration) error
	MinDelay time.Duration
}

type recipient struct {
	ID       uuid.UUID
	MAXID    int64
	Sequence int64
}

type deliveryStore interface {
	OptedInUsers(context.Context) ([]recipient, error)
	Claim(context.Context, string, uuid.UUID) (bool, error)
	OptedIn(context.Context, uuid.UUID) (bool, error)
	Cancel(context.Context, string, uuid.UUID) error
	Finish(context.Context, string, uuid.UUID, string, error) error
	MarkSent(context.Context, string, uuid.UUID) error
}

type postgresStore struct{ db *pgxpool.Pool }

func New(db *pgxpool.Pool, token, appURL string) (*Sender, error) {
	u, err := url.Parse(strings.TrimSpace(appURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("MAX_APP_URL must be an absolute https URL")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("MAX_BOT_TOKEN is required")
	}
	return &Sender{
		Store: postgresStore{db: db}, Client: &http.Client{Timeout: 15 * time.Second}, Token: token,
		AppURL: u.String(), BaseURL: maxAPIBaseURL, Now: time.Now, Sleep: sleepContext, MinDelay: 600 * time.Millisecond,
	}, nil
}

// RunOnce sends to users who opted in for the given Moscow-local date.
func (s *Sender) RunOnce(ctx context.Context, day time.Time) error {
	if s.Store == nil || s.Client == nil || s.Now == nil || s.Sleep == nil || s.BaseURL == "" {
		return errors.New("daily notification sender is not initialized")
	}
	date := day.In(moscowLocation()).Format("2006-01-02")
	users, err := s.Store.OptedInUsers(ctx)
	if err != nil {
		return fmt.Errorf("select opted-in users: %w", err)
	}

	failures := 0
	for i, user := range users {
		if i > 0 {
			if err := s.Sleep(ctx, s.MinDelay); err != nil {
				return err
			}
		}
		if err := s.deliver(ctx, date, user); err != nil {
			failures++
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d daily notification deliveries failed", failures)
	}
	return nil
}

func (s *Sender) deliver(ctx context.Context, date string, user recipient) error {
	claimed, err := s.Store.Claim(ctx, date, user.ID)
	if err != nil || !claimed {
		return err
	}
	if err := s.sendWithRetry(ctx, user); err != nil {
		status := "unknown"
		var deliveryErr *retryAfterError
		if errors.As(err, &deliveryErr) {
			if deliveryErr.permanent {
				status = "rejected"
			} else if deliveryErr.retryable {
				status = "failed"
			}
		}
		if errors.As(err, &deliveryErr) && deliveryErr.optedOut {
			return s.Store.Cancel(ctx, date, user.ID)
		}
		_ = s.Store.Finish(ctx, date, user.ID, status, err)
		return err
	}
	if err := s.Store.MarkSent(ctx, date, user.ID); err != nil {
		return fmt.Errorf("mark notification sent: %w", err)
	}
	return nil
}

func (p postgresStore) Claim(ctx context.Context, date string, userID uuid.UUID) (bool, error) {
	var claimed uuid.UUID
	err := p.db.QueryRow(ctx, `INSERT INTO daily_notification_deliveries(user_id, delivery_date, status, attempts, updated_at)
		VALUES($1,$2,'sending',1,now())
		ON CONFLICT(user_id, delivery_date) DO UPDATE SET status='sending', attempts=daily_notification_deliveries.attempts+1,
		last_error=NULL, updated_at=now()
		WHERE daily_notification_deliveries.status = 'failed'
		RETURNING user_id`, userID, date).Scan(&claimed)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim notification delivery: %w", err)
	}
	return true, nil
}

func (p postgresStore) Cancel(ctx context.Context, date string, userID uuid.UUID) error {
	_, err := p.db.Exec(ctx, `UPDATE daily_notification_deliveries SET status='rejected', last_error='user opted out', updated_at=now()
		WHERE user_id=$1 AND delivery_date=$2 AND status='sending'`, userID, date)
	return err
}

func (p postgresStore) Finish(ctx context.Context, date string, userID uuid.UUID, status string, cause error) error {
	message := "delivery error"
	var responseError *retryAfterError
	if errors.As(cause, &responseError) && responseError.statusCode > 0 {
		message = fmt.Sprintf("MAX API status %d", responseError.statusCode)
	}
	_, err := p.db.Exec(ctx, `UPDATE daily_notification_deliveries SET status=$3, last_error=$4, updated_at=now()
		WHERE user_id=$1 AND delivery_date=$2 AND status='sending'`, userID, date, status, message)
	return err
}

func (p postgresStore) OptedInUsers(ctx context.Context) ([]recipient, error) {
	rows, err := p.db.Query(ctx, `SELECT u.id, u.max_user_id, row_number() OVER (ORDER BY u.id)
		FROM users u WHERE u.daily_notifications_enabled = true ORDER BY u.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]recipient, 0)
	for rows.Next() {
		var user recipient
		if err := rows.Scan(&user.ID, &user.MAXID, &user.Sequence); err != nil {
			return nil, err
		}
		users = append(users, user)
	}
	return users, rows.Err()
}

func (p postgresStore) OptedIn(ctx context.Context, userID uuid.UUID) (bool, error) {
	var optedIn bool
	err := p.db.QueryRow(ctx, `SELECT daily_notifications_enabled FROM users WHERE id=$1`, userID).Scan(&optedIn)
	return optedIn, err
}

func (p postgresStore) MarkSent(ctx context.Context, date string, userID uuid.UUID) error {
	_, err := p.db.Exec(ctx, `UPDATE daily_notification_deliveries SET status='sent', sent_at=now(), last_error=NULL, updated_at=now()
		WHERE user_id=$1 AND delivery_date=$2 AND status='sending'`, userID, date)
	return err
}

func (s *Sender) sendWithRetry(ctx context.Context, user recipient) error {
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<uint(attempt-1)) * time.Second
			if lastErr != nil {
				var retry *retryAfterError
				if errors.As(lastErr, &retry) && retry.delay > wait {
					wait = retry.delay
				}
			}
			if err := s.Sleep(ctx, wait); err != nil {
				return err
			}
		}
		optedIn, err := s.Store.OptedIn(ctx, user.ID)
		if err != nil {
			return fmt.Errorf("check current notification consent: %w", err)
		}
		if !optedIn {
			return &retryAfterError{permanent: true, optedOut: true, message: "user opted out"}
		}
		lastErr = s.send(ctx, user)
		if lastErr == nil {
			return nil
		}
		var retry *retryAfterError
		if !errors.As(lastErr, &retry) || retry.permanent || !retry.retryable {
			break
		}
	}
	return lastErr
}

type retryAfterError struct {
	delay      time.Duration
	permanent  bool
	retryable  bool
	optedOut   bool
	statusCode int
	message    string
}

func (e *retryAfterError) Error() string { return e.message }

func (s *Sender) send(ctx context.Context, user recipient) error {
	text := prompts[(int(user.Sequence)-1+int(dayIndex(s.Now().In(moscowLocation()))))%len(prompts)]
	payload := map[string]any{
		"text": text,
		"attachments": []any{map[string]any{"type": "inline_keyboard", "payload": map[string]any{"buttons": [][]any{{map[string]any{
			"type": "open_app", "text": "Открыть приложение", "web_app": s.AppURL,
		}}}}}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fmt.Sprintf("%s/messages?user_id=%d", s.BaseURL, user.MAXID), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", s.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		_, _ = io.Copy(io.Discard, response.Body)
		return nil
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1000))
	message := fmt.Sprintf("MAX API returned status %d", response.StatusCode)
	if response.StatusCode == http.StatusTooManyRequests {
		return &retryAfterError{delay: parseRetryAfter(response.Header.Get("Retry-After"), s.Now()), retryable: true, statusCode: response.StatusCode, message: message}
	}
	if response.StatusCode >= 500 {
		return &retryAfterError{statusCode: response.StatusCode, message: message}
	}
	return &retryAfterError{permanent: true, statusCode: response.StatusCode, message: message}
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return 0
}

func dayIndex(now time.Time) int { return now.YearDay() }

func moscowLocation() *time.Location {
	loc, err := time.LoadLocation("Europe/Moscow")
	if err != nil {
		return time.FixedZone("MSK", 3*60*60)
	}
	return loc
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextNoon(now time.Time) time.Time {
	loc := moscowLocation()
	local := now.In(loc)
	next := time.Date(local.Year(), local.Month(), local.Day(), 12, 0, 0, 0, loc)
	if !local.Before(next) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
