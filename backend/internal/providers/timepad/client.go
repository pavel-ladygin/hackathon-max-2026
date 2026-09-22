// Package timepad imports Moscow events from the Timepad API.
package timepad

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
	"time"
)

const (
	maxErrorBodyBytes = 4 * 1024
	importHorizon     = 90 * 24 * time.Hour
	requestInterval   = 1100 * time.Millisecond
	moscowCity        = "Москва"
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
	query.Set("fields", "location,registration_data,ticket_types")
	requestURL.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return eventsPage{}, fmt.Errorf("create timepad request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.httpClient.Do(request)
	if err != nil {
		return eventsPage{}, fmt.Errorf("perform timepad request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return eventsPage{}, responseError(response)
	}

	var page eventsPage
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&page); err != nil {
		return eventsPage{}, fmt.Errorf("decode timepad response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return eventsPage{}, fmt.Errorf("decode timepad response: %w", err)
	}
	return page, nil
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
