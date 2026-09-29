// Package timepad imports Moscow events from the Timepad API.
package timepad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxErrorBodyBytes  = 4 * 1024
	importHorizon      = 90 * 24 * time.Hour
	maxRequestAttempts = 3
	retryBaseDelay     = time.Second
	maxRetryDelay      = 30 * time.Second
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
	baseURL    *url.URL
	token      string
	httpClient *http.Client
	pageSize   int
	limiter    requestLimiter
	now        func() time.Time
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
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/"
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: options.Timeout},
		pageSize:   options.PageSize,
		limiter:    newIntervalLimiter(options.MaxRequestsPerMinute),
		now:        time.Now,
		wait:       func(context.Context) error { return nil },
		waitRetry:  waitForRetry,
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
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return eventsPage{}, fmt.Errorf("create timepad request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	var page eventsPage
	response, err := c.doWithHandler(ctx, request, func(response *http.Response) error {
		decoder := json.NewDecoder(response.Body)
		if err := decoder.Decode(&page); err != nil {
			return err
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				return errors.New("multiple JSON values")
			}
			return err
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
	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		client := *c.httpClient
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if attempt == maxRequestAttempts {
				return nil, fmt.Errorf("perform timepad request after %d attempts: %w", attempt, err)
			}
			if err := c.waitRetry(ctx, c.retryDelay(attempt, nil)); err != nil {
				return nil, err
			}
			continue
		}
		if (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= http.StatusInternalServerError) && attempt < maxRequestAttempts {
			delay := c.retryDelay(attempt, response)
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
				return nil, fmt.Errorf("decode timepad response: %w", handleErr)
			}
			if attempt == maxRequestAttempts {
				return nil, fmt.Errorf("decode timepad response after %d attempts: %w", attempt, handleErr)
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
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
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
	body, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read timepad error response with status %d: %w", response.StatusCode, err)
	}
	truncated := len(body) > maxErrorBodyBytes
	if truncated {
		body = body[:maxErrorBodyBytes]
	}
	message := strings.TrimSpace(string(body))
	if truncated {
		message += " [truncated]"
	}
	if response.StatusCode == http.StatusTooManyRequests {
		if message == "" {
			return errors.New("timepad rate limit exceeded (status 429)")
		}
		return fmt.Errorf("timepad rate limit exceeded (status 429): %s", message)
	}
	if message == "" {
		return fmt.Errorf("timepad response status %d", response.StatusCode)
	}
	return fmt.Errorf("timepad response status %d: %s", response.StatusCode, message)
}
