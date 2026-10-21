// Package timepad imports Moscow events from the Timepad API.
package timepad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

const (
	importHorizon      = 90 * 24 * time.Hour
	maxRequestAttempts = 3
	retryBaseDelay     = time.Second
	maxRetryDelay      = 30 * time.Second
	maxRateLimitPause  = 30 * time.Second
	hardPageCeiling    = 10000
	moscowCity         = "Москва"
	requestedFields    = "location,registration_data,ticket_types,description_short,ends_at,created_at,age_limit,organization,access_status,moderation_status,poster_image"
)

var timepadMoscowLocation = time.FixedZone("Europe/Moscow", 3*60*60)

type Options struct {
	BaseURL              string
	Token                string
	Timeout              time.Duration
	PageSize             int
	MaxRequestsPerMinute int
	CityID               uuid.UUID
	CityName             string
	Timezone             string
	MaxPagesPerRun       int
	MaxRunDuration       time.Duration
	UpsertOnly           bool
}

type requestLimiter interface{ Wait(context.Context) error }

type intervalLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newIntervalLimiter(requestsPerMinute int) *intervalLimiter {
	return &intervalLimiter{interval: time.Minute / time.Duration(requestsPerMinute)}
}

func (l *intervalLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	now := time.Now()
	start := now
	if l.next.After(now) {
		start = l.next
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()
	delay := time.Until(start)
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

type Client struct {
	baseURL        *url.URL
	token          string
	httpClient     *http.Client
	pageSize       int
	cityID         uuid.UUID
	cityName       string
	timezone       string
	maxPagesPerRun int
	maxRunDuration time.Duration
	upsertOnly     bool
	attempts       atomic.Int64
	limiter        requestLimiter
	now            func() time.Time
	// wait is retained for importer pacing hooks; actual HTTP attempts use limiter.
	wait      func(context.Context) error
	waitRetry func(context.Context, time.Duration) error
	jitter    func(time.Duration) time.Duration
}

func NewClient(options Options) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || baseURL.Scheme != "https" || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("timepad base URL is invalid")
	}
	token := strings.TrimSpace(options.Token)
	if token == "" {
		return nil, errors.New("timepad token is required")
	}
	if options.Timeout <= 0 {
		return nil, errors.New("timepad timeout must be positive")
	}
	if options.PageSize < 1 || options.PageSize > 100 {
		return nil, errors.New("timepad page size must be between 1 and 100")
	}
	if options.MaxRequestsPerMinute <= 0 {
		return nil, errors.New("timepad max requests per minute must be positive")
	}
	if options.MaxPagesPerRun < 0 {
		return nil, errors.New("timepad max pages per run must not be negative")
	}
	if options.MaxRunDuration < 0 {
		return nil, errors.New("timepad max run duration must not be negative")
	}
	cityName := strings.TrimSpace(options.CityName)
	timezone := strings.TrimSpace(options.Timezone)
	if cityName == "" {
		cityName = moscowCity
	}
	if timezone == "" {
		timezone = moscowZone
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, errors.New("timepad timezone is invalid")
	}
	if options.CityID != uuid.Nil {
		if strings.TrimSpace(options.CityName) == "" {
			return nil, errors.New("timepad city name is required for configured city")
		}
		if strings.TrimSpace(options.Timezone) == "" {
			return nil, errors.New("timepad timezone is required for configured city")
		}
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/"
	if !strings.EqualFold(baseURL.Hostname(), "api.timepad.ru") || baseURL.Port() != "" {
		return nil, errors.New("timepad base URL must use api.timepad.ru")
	}
	return &Client{
		baseURL:        baseURL,
		token:          token,
		httpClient:     &http.Client{Timeout: options.Timeout},
		pageSize:       options.PageSize,
		cityID:         options.CityID,
		cityName:       cityName,
		timezone:       timezone,
		maxPagesPerRun: options.MaxPagesPerRun,
		maxRunDuration: options.MaxRunDuration,
		upsertOnly:     options.UpsertOnly,
		limiter:        newIntervalLimiter(options.MaxRequestsPerMinute),
		now:            time.Now,
		wait:           func(context.Context) error { return nil },
		waitRetry:      waitForRetry,
		jitter: func(delay time.Duration) time.Duration {
			return time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
		},
	}, nil
}

func (c *Client) fetchPage(ctx context.Context, skip int, startsAtMin, startsAtMax time.Time) (eventsPage, error) {
	requestURL := c.baseURL.ResolveReference(&url.URL{Path: "events.json"})
	query := requestURL.Query()
	query.Set("limit", strconv.Itoa(c.pageSize))
	query.Set("skip", strconv.Itoa(skip))
	query.Set("sort", "+starts_at")
	// Timepad date filters use Moscow wall-clock semantics even when an RFC3339
	// offset is present. Convert the intended instants before serialization so
	// the clock components Timepad applies still describe the correct boundary.
	query.Set("starts_at_min", startsAtMin.In(timepadMoscowLocation).Format(time.RFC3339))
	query.Set("starts_at_max", startsAtMax.In(timepadMoscowLocation).Format(time.RFC3339))
	query.Set("fields", requestedFields)
	query.Set("cities", c.effectiveCityName())
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return eventsPage{}, fmt.Errorf("create timepad request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	var page eventsPage
	response, err := c.doWithHandler(ctx, request, func(response *http.Response) error {
		decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
		if err := decoder.Decode(&page); err != nil {
			if retryableResponseReadError(err) {
				return err
			}
			if errors.Is(err, errInvalidPage) {
				return providers.SafeError("provider_pagination_error")
			}
			return providers.SafeError("provider_decode_error")
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if retryableResponseReadError(err) {
				return err
			}
			return providers.SafeError("provider_decode_error")
		}
		return nil
	})
	if err != nil {
		return eventsPage{}, fmt.Errorf("fetch timepad response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		responseErr := responseError(response)
		response.Body.Close()
		return eventsPage{}, responseErr
	}
	return page, nil
}

// do limits every physical request attempt, including retries. Redirects are
// returned to callers so public-page resolution can limit each hop itself.
func (c *Client) do(ctx context.Context, request *http.Request) (*http.Response, error) {
	return c.doWithHandler(ctx, request, nil)
}

func (c *Client) doWithHandler(ctx context.Context, request *http.Request, handle func(*http.Response) error) (*http.Response, error) {
	rateLimited := false
	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		c.attempts.Add(1)
		client := *c.httpClient
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == maxRequestAttempts {
				if providers.SyncErrorCode(err) == "provider_timeout" {
					return nil, providers.SafeError("provider_timeout")
				}
				return nil, providers.SafeError("provider_transport_error")
			}
			if err := c.waitRetry(ctx, c.retryDelay(attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode == http.StatusTooManyRequests {
			if rateLimited {
				return response, nil
			}
			rateLimited = true
		}
		if (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError) && attempt < maxRequestAttempts {
			delay := c.retryDelay(attempt, response)
			if response.StatusCode == http.StatusTooManyRequests && delay > maxRateLimitPause {
				return response, nil
			}
			response.Body.Close()
			if err := c.waitRetry(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return response, nil
		}
		if handle != nil {
			handleErr := handle(response)
			response.Body.Close()
			if handleErr == nil {
				return response, nil
			}
			if !retryableResponseReadError(handleErr) {
				return nil, handleErr
			}
			if attempt == maxRequestAttempts {
				if providers.SyncErrorCode(handleErr) == "provider_timeout" {
					return nil, providers.SafeError("provider_timeout")
				}
				return nil, providers.SafeError("provider_decode_error")
			}
			if err := c.waitRetry(ctx, c.retryDelay(attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		return response, nil
	}
	return nil, errors.New("timepad request attempts exhausted")
}

func (c *Client) retryDelay(attempt int, response *http.Response) time.Duration {
	if response != nil && response.StatusCode == http.StatusTooManyRequests {
		if delay, ok := retryAfter(response.Header.Get("Retry-After"), c.now()); ok {
			return delay
		}
	}
	delay := retryBaseDelay * time.Duration(1<<(attempt-1))
	if delay > maxRetryDelay {
		delay = maxRetryDelay
	}
	return c.jitter(delay)
}

func retryAfter(value string, now time.Time) (time.Duration, bool) {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds >= 0 {
		if seconds > math.MaxInt64/int64(time.Second) {
			return maxRateLimitPause + time.Second, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	delay := when.Sub(now)
	if delay < 0 {
		delay = 0
	}
	return delay, true
}

func retryableResponseReadError(err error) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func responseError(response *http.Response) error {
	// Response bodies can contain provider data or credentials. The shared HTTP
	// error preserves only the status for operational classification.
	return providers.HTTPError(response.StatusCode)
}
