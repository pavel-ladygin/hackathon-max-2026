// Package kudago contains the isolated transport client and private DTOs for
// the public KudaGo API. Provider normalization belongs to a later stage.
package kudago

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

	"github.com/google/uuid"
	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

const (
	maxErrorBodyBytes = 4 * 1024
	requestedFields   = "id,publication_date,title,short_title,tagline,description,body_text,dates,categories,age_restriction,price,is_free,images,site_url,location,place"
	requestedExpand   = "dates,images,location,place"
)

type Options struct {
	BaseURL              string
	Timeout              time.Duration
	Location             string
	PageSize             int
	MaxRequestsPerMinute int
	MaxPagesPerRun       int
	MaxRunDuration       time.Duration
	UpsertOnly           bool
	CityID               uuid.UUID
	Timezone             string
}

type Client struct {
	baseURL     *url.URL
	httpClient  *http.Client
	location    string
	cityID      uuid.UUID
	timezone    string
	pageSize    int
	maxPages    int
	maxDuration time.Duration
	upsertOnly  bool
	limiter     requestLimiter
	now         func() time.Time
	waitRetry   func(context.Context, time.Duration) error
	jitter      func(time.Duration) time.Duration
}

const (
	defaultRequestsPerMinute = 6
	defaultRunDuration       = 5 * time.Minute
	maxRequestAttempts       = 3
	maxRetryAfter            = 30 * time.Second
)

type requestLimiter interface{ Wait(context.Context) error }
type intervalLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

func newIntervalLimiter(perMinute int) *intervalLimiter {
	return &intervalLimiter{interval: time.Minute / time.Duration(perMinute)}
}
func (l *intervalLimiter) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	start := time.Now()
	if l.next.After(start) {
		start = l.next
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()
	if delay := time.Until(start); delay > 0 {
		return waitForRetry(ctx, delay)
	}
	return nil
}

type fetchOptions struct {
	Cursor      string
	ActualSince time.Time
	ActualUntil time.Time
}

func NewClient(options Options) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || !validBaseURL(baseURL) || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, errors.New("kudago base URL is invalid")
	}
	if options.Timeout <= 0 {
		return nil, errors.New("kudago timeout must be positive")
	}
	if strings.TrimSpace(options.Location) == "" {
		return nil, errors.New("kudago location is required")
	}
	if options.PageSize < 1 || options.PageSize > 100 {
		return nil, errors.New("kudago page size must be between 1 and 100")
	}
	if options.MaxPagesPerRun < 0 {
		return nil, errors.New("kudago max pages must not be negative")
	}
	requestsPerMinute := options.MaxRequestsPerMinute
	if requestsPerMinute <= 0 {
		requestsPerMinute = defaultRequestsPerMinute
	}
	maxDuration := options.MaxRunDuration
	if maxDuration <= 0 {
		maxDuration = defaultRunDuration
	}
	timezone := strings.TrimSpace(options.Timezone)
	if timezone == "" {
		timezone = moscowZone
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, errors.New("kudago timezone is invalid")
	}
	if options.CityID != uuid.Nil && strings.TrimSpace(options.Timezone) == "" {
		return nil, errors.New("kudago timezone is required for configured city")
	}
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/"
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSHandshakeTimeout = options.Timeout
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Transport: transport, Timeout: options.Timeout},
		location:   strings.TrimSpace(options.Location),
		cityID:     options.CityID,
		timezone:   timezone,
		pageSize:   options.PageSize,
		maxPages:   options.MaxPagesPerRun, maxDuration: maxDuration, upsertOnly: options.UpsertOnly,
		limiter:   newIntervalLimiter(requestsPerMinute),
		now:       time.Now,
		waitRetry: waitForRetry,
		jitter: func(delay time.Duration) time.Duration {
			return time.Duration(float64(delay) * (0.8 + rand.Float64()*0.4))
		},
	}, nil
}

func validBaseURL(u *url.URL) bool {
	if u == nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if u.Scheme == "https" && host == "kudago.com" && (u.Port() == "" || u.Port() == "443") {
		return true
	}
	// HTTP loopback is accepted solely for in-process test servers.
	return u.Scheme == "http" && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback())
}

func (c *Client) fetchPage(ctx context.Context, options fetchOptions) (eventsPage, error) {
	page, _, err := c.fetchPageWithAttempts(ctx, options)
	return page, err
}

func (c *Client) fetchPageWithAttempts(ctx context.Context, options fetchOptions) (eventsPage, int, error) {
	requestURL, err := c.requestURL(options)
	if err != nil {
		return eventsPage{}, 0, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return eventsPage{}, 0, fmt.Errorf("create kudago request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	response, attempts, err := c.do(ctx, request)
	if err != nil {
		return eventsPage{}, attempts, err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return eventsPage{}, attempts, responseError(response)
	}

	var page eventsPage
	decoder := json.NewDecoder(io.LimitReader(response.Body, 16<<20))
	if err := decoder.Decode(&page); err != nil {
		if providers.SyncErrorCode(err) == "provider_pagination_error" {
			return eventsPage{}, attempts, err
		}
		return eventsPage{}, attempts, providers.SafeError("provider_decode_error")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return eventsPage{}, attempts, providers.SafeError("provider_decode_error")
	}
	return page, attempts, nil
}

func (c *Client) do(ctx context.Context, request *http.Request) (*http.Response, int, error) {
	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, attempt - 1, err
		}
		client := *c.httpClient
		client.CheckRedirect = func(next *http.Request, _ []*http.Request) error {
			if !validBaseURL(next.URL) || !sameOrigin(c.baseURL, next.URL) {
				return errors.New("kudago redirect has unexpected origin")
			}
			return http.ErrUseLastResponse
		}
		response, err := client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return nil, attempt, ctx.Err()
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, attempt, providers.SafeError("provider_timeout")
			}
			if attempt == maxRequestAttempts {
				return nil, attempt, providers.SafeError("provider_transport_error")
			}
			if err := c.waitRetry(ctx, c.retryDelay(attempt, "")); err != nil {
				return nil, attempt, err
			}
			continue
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return response, attempt, nil
		}
		if response.StatusCode == http.StatusTooManyRequests {
			delay, ok := retryAfter(response.Header.Get("Retry-After"), c.now())
			response.Body.Close()
			if !ok || delay > maxRetryAfter || attempt >= 2 {
				return nil, attempt, providers.HTTPError(http.StatusTooManyRequests)
			}
			if err := c.waitRetry(ctx, delay); err != nil {
				return nil, attempt, err
			}
			continue
		}
		if response.StatusCode >= 500 && attempt < maxRequestAttempts {
			response.Body.Close()
			if err := c.waitRetry(ctx, c.retryDelay(attempt, "")); err != nil {
				return nil, attempt, err
			}
			continue
		}
		return response, attempt, nil
	}
	return nil, maxRequestAttempts, providers.SafeError("provider_transport_error")
}

func (c *Client) retryDelay(attempt int, _ string) time.Duration {
	delay := time.Second * time.Duration(1<<(attempt-1))
	if delay > maxRetryAfter {
		delay = maxRetryAfter
	}
	return c.jitter(delay)
}
func retryAfter(value string, now time.Time) (time.Duration, bool) {
	if n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && n >= 0 {
		if n > int64(maxRetryAfter/time.Second) {
			return 0, false
		}
		return time.Duration(n) * time.Second, true
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

func (c *Client) requestURL(options fetchOptions) (*url.URL, error) {
	if options.Cursor != "" {
		cursor, err := url.Parse(options.Cursor)
		if err != nil {
			return nil, providers.SafeError("provider_pagination_error")
		}
		if !cursor.IsAbs() {
			cursor = c.baseURL.ResolveReference(cursor)
		}
		if !validBaseURL(cursor) || !sameOrigin(c.baseURL, cursor) || cursor.User != nil {
			return nil, providers.SafeError("provider_origin_error")
		}
		if err := c.lockCursorScope(cursor, options); err != nil {
			return nil, err
		}
		return cursor, nil
	}

	requestURL := c.baseURL.ResolveReference(&url.URL{Path: "events/"})
	query := requestURL.Query()
	query.Set("lang", "ru")
	query.Set("location", c.location)
	query.Set("page_size", strconv.Itoa(c.pageSize))
	query.Set("fields", requestedFields)
	query.Set("expand", requestedExpand)
	query.Set("text_format", "text")
	if !options.ActualSince.IsZero() {
		query.Set("actual_since", strconv.FormatInt(options.ActualSince.Unix(), 10))
	}
	if !options.ActualUntil.IsZero() {
		query.Set("actual_until", strconv.FormatInt(options.ActualUntil.Unix(), 10))
	}
	requestURL.RawQuery = query.Encode()
	return requestURL, nil
}

// KudaGo cursor links are provider-controlled input. Preserve the original
// city/window constraints if a shorthand link omits them, and reject a link
// that attempts to widen or switch the configured scope.
func (c *Client) lockCursorScope(cursor *url.URL, options fetchOptions) error {
	query := cursor.Query()
	expected := map[string]string{
		"lang": "ru", "location": c.location, "page_size": strconv.Itoa(c.pageSize),
		"fields": requestedFields, "expand": requestedExpand, "text_format": "text",
	}
	if !options.ActualSince.IsZero() {
		expected["actual_since"] = strconv.FormatInt(options.ActualSince.Unix(), 10)
	}
	if !options.ActualUntil.IsZero() {
		expected["actual_until"] = strconv.FormatInt(options.ActualUntil.Unix(), 10)
	}
	for key, value := range expected {
		if got := query.Get(key); got != "" && got != value {
			return providers.SafeError("provider_pagination_error")
		}
		query.Set(key, value)
	}
	cursor.RawQuery = query.Encode()
	return nil
}

func sameOrigin(a, b *url.URL) bool {
	return a != nil && b != nil && a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

func responseError(response *http.Response) error {
	// Provider bodies can contain PII, URLs and arbitrary markup; diagnostics keep only status.
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBodyBytes))
	return providers.HTTPError(response.StatusCode)
}
