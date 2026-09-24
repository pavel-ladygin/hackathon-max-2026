// Package timepad imports Moscow events from the Timepad API.
package timepad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	maxErrorBodyBytes  = 4 * 1024
	importHorizon      = 90 * 24 * time.Hour
	requestInterval    = 1100 * time.Millisecond
	maxRequestAttempts = 3
	retryBaseDelay     = time.Second
	moscowCity         = "Москва"
	requestedFields    = "location,registration_data,ticket_types,description_short,ends_at,created_at,age_limit,organization,access_status,moderation_status,poster_image"
)

type Options struct {
	BaseURL  string
	Token    string
	Timeout  time.Duration
	PageSize int
}

type Client struct {
	baseURL    *url.URL
	token      string
	httpClient *http.Client
	pageSize   int
	now        func() time.Time
	wait       func(context.Context) error
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
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/"
	return &Client{
		baseURL:    baseURL,
		token:      token,
		httpClient: &http.Client{Timeout: options.Timeout},
		pageSize:   options.PageSize,
		now:        time.Now,
		wait: func(ctx context.Context) error {
			timer := time.NewTimer(requestInterval)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}, nil
}

func (c *Client) fetchPage(ctx context.Context, skip int, startsAtMin, startsAtMax time.Time) (eventsPage, error) {
	requestURL := c.baseURL.ResolveReference(&url.URL{Path: "events.json"})
	query := requestURL.Query()
	query.Set("limit", strconv.Itoa(c.pageSize))
	query.Set("skip", strconv.Itoa(skip))
	query.Set("sort", "+starts_at")
	query.Set("starts_at_min", startsAtMin.Format(time.RFC3339))
	query.Set("starts_at_max", startsAtMax.Format(time.RFC3339))
	query.Set("fields", requestedFields)
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return eventsPage{}, fmt.Errorf("create timepad request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	for attempt := 1; attempt <= maxRequestAttempts; attempt++ {
		response, err := c.httpClient.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return eventsPage{}, ctx.Err()
			}
			if attempt == maxRequestAttempts {
				return eventsPage{}, fmt.Errorf("perform timepad request after %d attempts: %w", attempt, err)
			}
			if err := waitForRetry(ctx, retryBaseDelay*time.Duration(1<<(attempt-1))); err != nil {
				return eventsPage{}, err
			}
			continue
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			statusCode := response.StatusCode
			responseErr := responseError(response)
			response.Body.Close()

			if statusCode >= http.StatusInternalServerError && attempt < maxRequestAttempts {
				if err := waitForRetry(ctx, retryBaseDelay*time.Duration(1<<(attempt-1))); err != nil {
					return eventsPage{}, err
				}
				continue
			}
			return eventsPage{}, responseErr
		}

		var page eventsPage
		decoder := json.NewDecoder(response.Body)
		decodeErr := decoder.Decode(&page)
		if decodeErr == nil {
			var extra any
			if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
				decodeErr = err
				if decodeErr == nil {
					decodeErr = errors.New("multiple JSON values")
				}
			}
		}
		response.Body.Close()
		if decodeErr != nil {
			if ctx.Err() != nil {
				return eventsPage{}, ctx.Err()
			}
			if retryableResponseReadError(decodeErr) && attempt < maxRequestAttempts {
				if err := waitForRetry(ctx, retryBaseDelay*time.Duration(1<<(attempt-1))); err != nil {
					return eventsPage{}, err
				}
				continue
			}
			if retryableResponseReadError(decodeErr) {
				return eventsPage{}, fmt.Errorf("decode timepad response after %d attempts: %w", attempt, decodeErr)
			}
			return eventsPage{}, fmt.Errorf("decode timepad response: %w", decodeErr)
		}
		return page, nil
	}

	return eventsPage{}, errors.New("timepad request attempts exhausted")
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
