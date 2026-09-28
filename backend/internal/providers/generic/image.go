package generic

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"time"
)

const ImageMaxResponseBytes = 8 << 20

// FetchImage fetches a validated raster image from a public HTTPS URL. It sends
// no provider credentials, cookies, or proxy-derived traffic and sanitizes all
// failures so neither URL nor upstream response details escape.
func FetchImage(ctx context.Context, rawURL string) ([]byte, string, error) {
	return fetchImageWithDependencies(ctx, rawURL, defaultResolve, (&net.Dialer{}).DialContext, nil)
}

func fetchImageWithDependencies(ctx context.Context, rawURL string, resolve ipResolver, dial contextDialer, tlsConfig *tls.Config) ([]byte, string, error) {
	return fetchImageWithTimeout(ctx, rawURL, resolve, dial, tlsConfig, HTTPTimeout)
}

func fetchImageWithTimeout(ctx context.Context, rawURL string, resolve ipResolver, dial contextDialer, tlsConfig *tls.Config, timeout time.Duration) ([]byte, string, error) {
	endpoint, err := validateEndpoint(rawURL)
	if err != nil {
		return nil, "", errors.New("image URL must be a valid public HTTPS URL")
	}
	transport, err := secureTransport(resolve, dial, tlsConfig)
	if err != nil {
		return nil, "", errors.New("image fetch unavailable")
	}
	defer transport.CloseIdleConnections()
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", errors.New("image request failed")
	}
	response, err := secureClient(transport).Do(request)
	if err != nil {
		return nil, "", errors.New("image request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", errors.New("image upstream returned an unsuccessful status")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, ImageMaxResponseBytes+1))
	if err != nil {
		return nil, "", errors.New("image response could not be read")
	}
	if len(body) > ImageMaxResponseBytes {
		return nil, "", errors.New("image response exceeds size limit")
	}
	mediaType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || !validImageType(mediaType) || !imageSignatureMatches(mediaType, body) {
		return nil, "", errors.New("image response has an unsupported content type")
	}
	return body, mediaType, nil
}

func validImageType(value string) bool {
	switch strings.ToLower(value) {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif":
		return true
	default:
		return false
	}
}

func imageSignatureMatches(mediaType string, body []byte) bool {
	switch strings.ToLower(mediaType) {
	case "image/jpeg":
		return len(body) >= 3 && body[0] == 0xff && body[1] == 0xd8 && body[2] == 0xff
	case "image/png":
		return len(body) >= 8 && string(body[:8]) == "\x89PNG\r\n\x1a\n"
	case "image/gif":
		return len(body) >= 6 && (string(body[:6]) == "GIF87a" || string(body[:6]) == "GIF89a")
	case "image/webp":
		return len(body) >= 12 && string(body[:4]) == "RIFF" && string(body[8:12]) == "WEBP"
	case "image/avif":
		return len(body) >= 12 && string(body[4:8]) == "ftyp" && (strings.HasPrefix(string(body[8:]), "avif") || strings.HasPrefix(string(body[8:]), "avis"))
	default:
		return false
	}
}
