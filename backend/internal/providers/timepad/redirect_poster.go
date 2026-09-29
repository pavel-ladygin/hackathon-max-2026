package timepad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

const (
	redirectPosterTimeout        = 10 * time.Second
	redirectPosterMaxHops        = 5
	redirectPosterMaxAPIResponse = 1 << 20
)

var timepadEventPathPattern = regexp.MustCompile(`(?i)(?:^|/)event/(\d+)/?$`)

// ResolveRedirectPoster looks up a poster on a canonical Timepad event when
// the current event's API response has no poster. It is intended for an
// explicit bounded backfill; it is not called by the regular importer.
func (c *Client) ResolveRedirectPoster(ctx context.Context, currentEventID int64, eventURL string) ([]providers.NormalizedImage, error) {
	_, images, err := c.ResolveRedirectPosterWithCanonicalID(ctx, currentEventID, eventURL)
	return images, err
}

// ResolveRedirectPosterWithCanonicalID also returns the final event ID, so a
// bounded backfill can reuse one API lookup for multiple events in a series.
func (c *Client) ResolveRedirectPosterWithCanonicalID(ctx context.Context, currentEventID int64, eventURL string) (int64, []providers.NormalizedImage, error) {
	if c == nil {
		return 0, nil, errors.New("timepad client is required")
	}
	requestCtx, cancel := context.WithTimeout(ctx, redirectPosterTimeout)
	defer cancel()
	canonicalID, err := c.resolveCanonicalEventID(requestCtx, currentEventID, eventURL)
	if err != nil || canonicalID == 0 {
		return canonicalID, nil, err
	}
	images, err := c.fetchEventPoster(requestCtx, canonicalID)
	return canonicalID, images, err
}

// ResolveCanonicalEventID follows a public event URL and returns a different
// canonical event ID when Timepad redirects the page to another event.
func (c *Client) ResolveCanonicalEventID(ctx context.Context, currentEventID int64, eventURL string) (int64, error) {
	if c == nil {
		return 0, errors.New("timepad client is required")
	}
	requestCtx, cancel := context.WithTimeout(ctx, redirectPosterTimeout)
	defer cancel()
	return c.resolveCanonicalEventID(requestCtx, currentEventID, eventURL)
}

// FetchEventPoster fetches and normalizes the poster for a canonical event ID.
func (c *Client) FetchEventPoster(ctx context.Context, canonicalEventID int64) ([]providers.NormalizedImage, error) {
	if c == nil {
		return nil, errors.New("timepad client is required")
	}
	requestCtx, cancel := context.WithTimeout(ctx, redirectPosterTimeout)
	defer cancel()
	return c.fetchEventPoster(requestCtx, canonicalEventID)
}

func (c *Client) resolveCanonicalEventID(ctx context.Context, currentEventID int64, eventURL string) (int64, error) {
	if currentEventID <= 0 {
		return 0, errors.New("timepad event ID must be positive")
	}
	current, err := url.Parse(strings.TrimSpace(eventURL))
	if err != nil || !validPublicTimepadURL(current) {
		return 0, errors.New("timepad event URL is invalid")
	}
	for hops := 0; ; hops++ {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.String(), nil)
		if err != nil {
			return 0, fmt.Errorf("create timepad page request: %w", err)
		}
		request.Header.Set("Accept", "text/html")
		response, err := c.do(ctx, request)
		if err != nil {
			return 0, fmt.Errorf("request timepad event page: %w", err)
		}
		response.Body.Close()
		if response.StatusCode >= http.StatusMultipleChoices && response.StatusCode < http.StatusMultipleChoices+100 {
			if hops >= redirectPosterMaxHops {
				return 0, errors.New("timepad page redirect is not allowed")
			}
			next, err := current.Parse(response.Header.Get("Location"))
			if err != nil || !validPublicTimepadURL(next) {
				return 0, errors.New("timepad page redirect is not allowed")
			}
			current = next
			continue
		}
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			return 0, fmt.Errorf("timepad event page returned HTTP %d", response.StatusCode)
		}
		parentID, err := timepadEventIDFromURL(current)
		if err != nil {
			return 0, err
		}
		if parentID == currentEventID {
			return 0, nil
		}
		return parentID, nil
	}
}

func (c *Client) fetchEventPoster(ctx context.Context, canonicalEventID int64) ([]providers.NormalizedImage, error) {
	if canonicalEventID <= 0 {
		return nil, errors.New("canonical timepad event ID must be positive")
	}
	apiURL := c.baseURL.ResolveReference(&url.URL{Path: "events/" + strconv.FormatInt(canonicalEventID, 10) + ".json"})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create timepad API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	response, err := c.do(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("request redirected timepad event: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("redirected timepad event API returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, redirectPosterMaxAPIResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read redirected timepad event: %w", err)
	}
	if len(body) > redirectPosterMaxAPIResponse {
		return nil, errors.New("redirected timepad event response is too large")
	}
	var parent struct {
		ID          int64    `json:"id"`
		PosterImage imageDTO `json:"poster_image"`
	}
	if err := json.Unmarshal(body, &parent); err != nil {
		return nil, fmt.Errorf("decode redirected timepad event: %w", err)
	}
	if parent.ID != canonicalEventID {
		return nil, errors.New("redirected timepad API returned an unexpected event ID")
	}
	return normalizeImages(parent.PosterImage), nil
}

func resolveCanonicalEventID(ctx context.Context, currentEventID int64, eventURL string, pageClient *http.Client) (int64, error) {
	if currentEventID <= 0 {
		return 0, errors.New("timepad event ID must be positive")
	}
	parsedEventURL, err := url.Parse(strings.TrimSpace(eventURL))
	if err != nil || !validPublicTimepadURL(parsedEventURL) {
		return 0, errors.New("timepad event URL is invalid")
	}
	if pageClient == nil {
		return 0, errors.New("timepad page HTTP client is required")
	}
	pageRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedEventURL.String(), nil)
	if err != nil {
		return 0, fmt.Errorf("create timepad page request: %w", err)
	}
	pageRequest.Header.Set("Accept", "text/html")
	pageHTTPClient := *pageClient
	priorCheckRedirect := pageClient.CheckRedirect
	pageHTTPClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > redirectPosterMaxHops || !validPublicTimepadURL(request.URL) {
			return errors.New("timepad page redirect is not allowed")
		}
		if priorCheckRedirect != nil {
			return priorCheckRedirect(request, via)
		}
		return nil
	}
	pageResponse, err := pageHTTPClient.Do(pageRequest)
	if err != nil {
		return 0, fmt.Errorf("request timepad event page: %w", err)
	}
	pageResponse.Body.Close()
	if pageResponse.StatusCode < http.StatusOK || pageResponse.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("timepad event page returned HTTP %d", pageResponse.StatusCode)
	}
	if pageResponse.Request == nil || !validPublicTimepadURL(pageResponse.Request.URL) {
		return 0, errors.New("timepad event page resolved to a disallowed URL")
	}
	parentID, err := timepadEventIDFromURL(pageResponse.Request.URL)
	if err != nil {
		return 0, err
	}
	if parentID == currentEventID {
		return 0, nil
	}
	return parentID, nil
}

func fetchEventPoster(ctx context.Context, canonicalEventID int64, token string, apiBaseURL *url.URL, apiClient *http.Client) ([]providers.NormalizedImage, error) {
	if canonicalEventID <= 0 {
		return nil, errors.New("canonical timepad event ID must be positive")
	}
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("timepad token is required")
	}
	if apiBaseURL == nil || apiBaseURL.Scheme != "https" || apiBaseURL.Host == "" || apiBaseURL.User != nil || apiClient == nil {
		return nil, errors.New("timepad API HTTP client is required")
	}
	apiURL := apiBaseURL.ResolveReference(&url.URL{Path: "events/" + strconv.FormatInt(canonicalEventID, 10) + ".json"})
	apiRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create timepad API request: %w", err)
	}
	apiRequest.Header.Set("Accept", "application/json")
	apiRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	apiHTTPClient := *apiClient
	apiHTTPClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	apiResponse, err := apiHTTPClient.Do(apiRequest)
	if err != nil {
		return nil, fmt.Errorf("request redirected timepad event: %w", err)
	}
	defer apiResponse.Body.Close()
	if apiResponse.StatusCode < http.StatusOK || apiResponse.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("redirected timepad event API returned HTTP %d", apiResponse.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(apiResponse.Body, redirectPosterMaxAPIResponse+1))
	if err != nil {
		return nil, fmt.Errorf("read redirected timepad event: %w", err)
	}
	if len(body) > redirectPosterMaxAPIResponse {
		return nil, errors.New("redirected timepad event response is too large")
	}
	var parent struct {
		ID          int64    `json:"id"`
		PosterImage imageDTO `json:"poster_image"`
	}
	if err := json.Unmarshal(body, &parent); err != nil {
		return nil, fmt.Errorf("decode redirected timepad event: %w", err)
	}
	if parent.ID != canonicalEventID {
		return nil, errors.New("redirected timepad API returned an unexpected event ID")
	}
	return normalizeImages(parent.PosterImage), nil
}

// resolveRedirectPoster composes canonical-page resolution and API poster
// lookup while keeping both requests under one overall timeout.
func resolveRedirectPoster(
	ctx context.Context,
	eventURL string,
	currentEventID int64,
	token string,
	apiBaseURL *url.URL,
	pageClient *http.Client,
	apiClient *http.Client,
) (int64, []providers.NormalizedImage, error) {
	requestCtx, cancel := context.WithTimeout(ctx, redirectPosterTimeout)
	defer cancel()
	canonicalID, err := resolveCanonicalEventID(requestCtx, currentEventID, eventURL, pageClient)
	if err != nil || canonicalID == 0 {
		return canonicalID, nil, err
	}
	images, err := fetchEventPoster(requestCtx, canonicalID, token, apiBaseURL, apiClient)
	return canonicalID, images, err
}

func validPublicTimepadURL(parsed *url.URL) bool {
	if parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Opaque != "" {
		return false
	}
	if parsed.Port() != "" && parsed.Port() != "443" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	return host == "timepad.ru" || strings.HasSuffix(host, ".timepad.ru")
}

func timepadEventIDFromURL(parsed *url.URL) (int64, error) {
	if !validPublicTimepadURL(parsed) {
		return 0, errors.New("timepad event URL is invalid")
	}
	match := timepadEventPathPattern.FindStringSubmatch(parsed.Path)
	if len(match) != 2 {
		return 0, errors.New("timepad page URL does not identify an event")
	}
	id, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("timepad page URL contains an invalid event ID")
	}
	return id, nil
}
