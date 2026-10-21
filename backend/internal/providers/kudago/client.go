// Package kudago contains the isolated transport client and private DTOs for
// the public KudaGo API. Provider normalization belongs to a later stage.
package kudago

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	CityID               uuid.UUID
	Timezone             string
}

type Client struct {
	baseURL       *url.URL
	httpClient    *http.Client
	location      string
	cityID        uuid.UUID
	timezone      string
	pageSize      int
	now           func() time.Time
	wallNow       func() time.Time
	wait          func(context.Context, time.Duration) error
	interval      time.Duration
	mu            sync.Mutex
	nextRequest   time.Time
	cooldownUntil time.Time
}

type fetchOptions struct {
	Cursor      string
	ActualSince time.Time
	ActualUntil time.Time
}

func NewClient(options Options) (*Client, error) {
	baseURL, err := url.Parse(strings.TrimSpace(options.BaseURL))
	if err != nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") || baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
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
	if options.MaxRequestsPerMinute == 0 {
		options.MaxRequestsPerMinute = 6
	}
	if options.MaxRequestsPerMinute < 1 || options.MaxRequestsPerMinute > 60 {
		return nil, errors.New("kudago max requests per minute must be between 1 and 60")
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
		httpClient: &http.Client{Transport: transport, Timeout: options.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		location:   strings.TrimSpace(options.Location),
		cityID:     options.CityID,
		timezone:   timezone,
		pageSize:   options.PageSize,
		now:        time.Now,
		wallNow:    time.Now,
		wait:       waitFor,
		interval:   time.Minute / time.Duration(options.MaxRequestsPerMinute),
	}, nil
}

func (c *Client) fetchPage(ctx context.Context, options fetchOptions) (eventsPage, error) {
	requestURL, err := c.requestURL(options)
	if err != nil {
		return eventsPage{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return eventsPage{}, fmt.Errorf("create kudago request: %w", err)
	}
	request.Header.Set("Accept", "application/json")

	if err := c.pace(ctx); err != nil {
		return eventsPage{}, err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return eventsPage{}, ctx.Err()
		}
		code := "kudago_transport_error"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "kudago_timeout"
		}
		return eventsPage{}, &syncError{code: code, err: fmt.Errorf("perform kudago request: %w", err)}
	}
	defer response.Body.Close()

	if response.StatusCode == http.StatusTooManyRequests {
		c.setCooldown(response.Header.Get("Retry-After"))
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return eventsPage{}, responseError(response)
	}

	var page eventsPage
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&page); err != nil {
		return eventsPage{}, fmt.Errorf("decode kudago response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return eventsPage{}, fmt.Errorf("decode kudago response: %w", err)
	}
	return page, nil
}

func (c *Client) requestURL(options fetchOptions) (*url.URL, error) {
	if options.Cursor != "" {
		cursor, err := url.Parse(options.Cursor)
		if err != nil {
			return nil, fmt.Errorf("parse kudago pagination URL: %w", err)
		}
		if !cursor.IsAbs() {
			cursor = c.baseURL.ResolveReference(cursor)
		}
		if cursor.Scheme != c.baseURL.Scheme || !strings.EqualFold(cursor.Host, c.baseURL.Host) || cursor.User != nil {
			return nil, errors.New("kudago pagination URL has unexpected origin")
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

func (c *Client) pace(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.wallNow()
	if now.Before(c.cooldownUntil) {
		return &syncError{code: "kudago_http_429", err: errors.New("kudago request is cooling down")}
	}
	start := now
	if c.nextRequest.After(start) {
		start = c.nextRequest
	}
	if err := c.wait(ctx, start.Sub(now)); err != nil {
		return err
	}
	c.nextRequest = start.Add(c.interval)
	return nil
}

func (c *Client) setCooldown(retryAfter string) {
	delay := retryAfterDelay(retryAfter, c.wallNow())
	c.mu.Lock()
	defer c.mu.Unlock()
	until := c.wallNow().Add(delay)
	if until.After(c.cooldownUntil) {
		c.cooldownUntil = until
	}
}

func retryAfterDelay(value string, now time.Time) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
		if seconds > int64((time.Duration(1<<63-1))/time.Second) {
			return time.Duration(1<<63 - 1)
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil && at.After(now) {
		return at.Sub(now)
	}
	return time.Minute
}

func waitFor(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
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

type syncError struct {
	code string
	err  error
}

func (e *syncError) Error() string         { return e.code }
func (e *syncError) Unwrap() error         { return e.err }
func (e *syncError) SyncErrorCode() string { return e.code }

func responseError(response *http.Response) error {
	return &syncError{code: fmt.Sprintf("kudago_http_%d", response.StatusCode), err: fmt.Errorf("kudago response status %d", response.StatusCode)}
}
