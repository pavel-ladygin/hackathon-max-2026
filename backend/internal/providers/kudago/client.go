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
	"time"
)

const (
	maxErrorBodyBytes = 4 * 1024
	requestedFields   = "id,publication_date,title,short_title,tagline,description,body_text,dates,categories,age_restriction,price,is_free,images,site_url,location,place"
	requestedExpand   = "dates,images,location,place"
)

type Options struct {
	BaseURL  string
	Timeout  time.Duration
	Location string
	PageSize int
}

type Client struct {
	baseURL    *url.URL
	httpClient *http.Client
	location   string
	pageSize   int
	now        func() time.Time
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
	baseURL.Path = strings.TrimRight(baseURL.Path, "/") + "/"
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: options.Timeout},
		location:   strings.TrimSpace(options.Location),
		pageSize:   options.PageSize,
		now:        time.Now,
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

	response, err := c.httpClient.Do(request)
	if err != nil {
		return eventsPage{}, fmt.Errorf("perform kudago request: %w", err)
	}
	defer response.Body.Close()

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

func responseError(response *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(response.Body, maxErrorBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read kudago error response with status %d: %w", response.StatusCode, err)
	}
	truncated := len(body) > maxErrorBodyBytes
	if truncated {
		body = body[:maxErrorBodyBytes]
	}
	message := strings.TrimSpace(string(body))
	if truncated {
		message += " [truncated]"
	}
	if message == "" {
		return fmt.Errorf("kudago response status %d", response.StatusCode)
	}
	return fmt.Errorf("kudago response status %d: %s", response.StatusCode, message)
}
