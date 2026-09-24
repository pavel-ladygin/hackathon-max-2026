package timepad

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/pavel-ladygin/hackathon-max-2026/backend/internal/providers"
)

func TestResolveRedirectPosterUsesCanonicalEventPoster(t *testing.T) {
	var pageRequests, apiRequests int
	pageClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		pageRequests++
		if request.Header.Get("Authorization") != "" {
			t.Fatal("public page request unexpectedly included Authorization")
		}
		switch request.URL.Path {
		case "/event/4176640/":
			result := redirectResponse(http.StatusFound, "https://eurogym.timepad.ru/event/2956317/")
			result.Request = request
			return result, nil
		case "/event/2956317/":
			result := response(http.StatusOK, "")
			result.Request = request
			return result, nil
		default:
			t.Fatalf("unexpected public page URL: %s", request.URL)
			return nil, nil
		}
	})}
	apiClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		apiRequests++
		if request.URL.String() != "https://api.timepad.test/v1/events/2956317.json" {
			t.Fatalf("API URL = %s", request.URL)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("API Authorization = %q", got)
		}
		result := response(http.StatusOK, `{"id":2956317,"poster_image":{"default_url":"https://ucare.timepad.ru/poster.jpg"}}`)
		result.Request = request
		return result, nil
	})}
	apiBaseURL, _ := url.Parse("https://api.timepad.test/v1/")

	parentID, images, err := resolveRedirectPoster(context.Background(), "https://eurogym.timepad.ru/event/4176640/", 4176640, "secret", apiBaseURL, pageClient, apiClient)
	if err != nil {
		t.Fatal(err)
	}
	want := []providers.NormalizedImage{{URL: "https://ucare.timepad.ru/poster.jpg", Role: "card", Position: 0}}
	if len(images) != 1 || images[0] != want[0] {
		t.Fatalf("images = %+v, want %+v", images, want)
	}
	if parentID != 2956317 {
		t.Fatalf("canonical ID = %d, want 2956317", parentID)
	}
	if pageRequests != 2 || apiRequests != 1 {
		t.Fatalf("request counts = page %d, API %d", pageRequests, apiRequests)
	}
}

func TestCanonicalResolutionAndPosterFetchAreSeparate(t *testing.T) {
	pageClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/event/4176640/" {
			result := redirectResponse(http.StatusFound, "https://eurogym.timepad.ru/event/2956317/")
			result.Request = request
			return result, nil
		}
		result := response(http.StatusOK, "")
		result.Request = request
		return result, nil
	})}
	canonicalID, err := resolveCanonicalEventID(context.Background(), 4176640, "https://eurogym.timepad.ru/event/4176640/", pageClient)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalID != 2956317 {
		t.Fatalf("canonical ID = %d, want 2956317", canonicalID)
	}

	apiClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/v1/events/2956317.json" {
			t.Fatalf("API path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("API Authorization = %q", got)
		}
		return response(http.StatusOK, `{"id":2956317,"poster_image":{"default_url":"https://ucare.timepad.ru/poster.jpg"}}`), nil
	})}
	images, err := fetchEventPoster(context.Background(), canonicalID, "secret", mustURL(t, "https://api.timepad.test/v1/"), apiClient)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || images[0].URL != "https://ucare.timepad.ru/poster.jpg" {
		t.Fatalf("images = %+v", images)
	}
}

func TestFetchEventPosterDoesNotForwardTokenAcrossAPIRedirect(t *testing.T) {
	var requests int
	apiClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if request.URL.Host != "api.timepad.test" {
			t.Fatalf("request followed redirect to unexpected host %q", request.URL.Host)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("API Authorization = %q", got)
		}
		return redirectResponse(http.StatusFound, "https://attacker.example/poster.json"), nil
	})}

	images, err := fetchEventPoster(context.Background(), 2956317, "secret", mustURL(t, "https://api.timepad.test/v1/"), apiClient)
	if err == nil {
		t.Fatal("fetchEventPoster() error = nil, want redirect status error")
	}
	if images != nil || requests != 1 {
		t.Fatalf("images = %+v, requests = %d; want nil images and one request", images, requests)
	}
}

func TestResolveRedirectPosterSkipsSameEventID(t *testing.T) {
	apiRequests := 0
	pageClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/event/1/" {
			result := redirectResponse(http.StatusFound, "https://timepad.ru/event/2/")
			result.Request = request
			return result, nil
		}
		result := response(http.StatusOK, "")
		result.Request = request
		return result, nil
	})}
	apiClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		apiRequests++
		return response(http.StatusOK, `{}`), nil
	})}
	apiBaseURL, _ := url.Parse("https://api.timepad.test/v1/")

	parentID, images, err := resolveRedirectPoster(context.Background(), "https://timepad.ru/event/123/", 123, "secret", apiBaseURL, pageClient, apiClient)
	if err != nil {
		t.Fatal(err)
	}
	if parentID != 0 || images != nil || apiRequests != 0 {
		t.Fatalf("canonical ID = %d, images = %+v, API requests = %d; want zero, nil, zero", parentID, images, apiRequests)
	}
}

func TestResolveRedirectPosterRejectsUnsafeRedirect(t *testing.T) {
	apiRequests := 0
	pageClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		result := redirectResponse(http.StatusFound, "https://timepad.ru.evil.example/event/2/")
		result.Request = request
		return result, nil
	})}
	apiClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		apiRequests++
		return response(http.StatusOK, `{}`), nil
	})}
	apiBaseURL, _ := url.Parse("https://api.timepad.test/v1/")

	_, _, err := resolveRedirectPoster(context.Background(), "https://timepad.ru/event/1/", 1, "secret", apiBaseURL, pageClient, apiClient)
	if err == nil || apiRequests != 0 {
		t.Fatalf("error = %v, API requests = %d; want redirect error and zero API requests", err, apiRequests)
	}
}

func TestResolveRedirectPosterRejectsNonHTTPSInitialURL(t *testing.T) {
	_, _, err := resolveRedirectPoster(context.Background(), "http://timepad.ru/event/1/", 1, "secret", mustURL(t, "https://api.timepad.test/v1/"), &http.Client{}, &http.Client{})
	if err == nil {
		t.Fatal("expected invalid initial URL error")
	}
}

func TestResolveRedirectPosterBoundsAPIResponse(t *testing.T) {
	pageClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path == "/event/1/" {
			result := redirectResponse(http.StatusFound, "https://timepad.ru/event/2/")
			result.Request = request
			return result, nil
		}
		result := response(http.StatusOK, "")
		result.Request = request
		return result, nil
	})}
	apiClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, strings.Repeat("x", redirectPosterMaxAPIResponse+1)), nil
	})}

	_, _, err := resolveRedirectPoster(context.Background(), "https://timepad.ru/event/1/", 1, "secret", mustURL(t, "https://api.timepad.test/v1/"), pageClient, apiClient)
	if err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("error = %v, want oversized response error", err)
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func redirectResponse(status int, location string) *http.Response {
	result := response(status, "")
	result.Header.Set("Location", location)
	return result
}
