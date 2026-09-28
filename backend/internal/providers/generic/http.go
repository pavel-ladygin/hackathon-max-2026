package generic

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// HTTPTimeout bounds DNS resolution, connection setup, and response reading.
	HTTPTimeout = 15 * time.Second
	// HTTPMaxResponseBytes bounds the decoded JSON response body.
	HTTPMaxResponseBytes = 2 << 20
)

type AuthMode string

const (
	AuthNone         AuthMode = "none"
	AuthBearer       AuthMode = "bearer"
	AuthAPIKeyHeader AuthMode = "api_key_header"
	AuthAPIKeyQuery  AuthMode = "api_key_query"
)

// AuthConfig describes one supported authentication mechanism.
type AuthConfig struct {
	Mode       AuthMode
	Token      string
	Key        string
	HeaderName string
	QueryName  string
}

// HTTPConfig describes one JSON endpoint and its fixed request parameters.
type HTTPConfig struct {
	Endpoint string
	Query    url.Values
	Auth     AuthConfig
}

type ipResolver func(context.Context, string) ([]netip.Addr, error)
type contextDialer func(context.Context, string, string) (net.Conn, error)

var blockedIPv4Prefixes = mustPrefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8",
	"169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24",
	"192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24",
	"203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
)

var blockedIPv6Prefixes = mustPrefixes(
	"2001::/23", "2001:db8::/32", "2002::/16", "3fff::/20",
)

func mustPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefixes = append(prefixes, netip.MustParsePrefix(value))
	}
	return prefixes
}

// FetchJSON fetches and decodes exactly one JSON value from a public HTTPS endpoint.
// It intentionally returns only sanitized errors so endpoint parameters and credentials
// cannot escape through transport or server error messages.
func FetchJSON(ctx context.Context, config HTTPConfig) (any, error) {
	return fetchJSONWithDependencies(ctx, config, defaultResolve, (&net.Dialer{}).DialContext)
}

// FetchJSONWithStatus also returns the actual upstream status without exposing its body or URL in errors.
func FetchJSONWithStatus(ctx context.Context, config HTTPConfig) (any, int, error) {
	var status int
	value, err := fetchJSONWithTLSConfigStatus(ctx, config, defaultResolve, (&net.Dialer{}).DialContext, nil, &status)
	return value, status, err
}

// ValidateHTTPConfig checks the endpoint and authentication settings without
// issuing a network request.
func ValidateHTTPConfig(config HTTPConfig) error {
	if _, err := validateEndpoint(config.Endpoint); err != nil {
		return err
	}
	return applyAuth(url.Values{}, config.Auth)
}

func fetchJSONWithDependencies(ctx context.Context, config HTTPConfig, resolve ipResolver, dial contextDialer) (any, error) {
	return fetchJSONWithTLSConfig(ctx, config, resolve, dial, nil)
}

func fetchJSONWithTLSConfig(ctx context.Context, config HTTPConfig, resolve ipResolver, dial contextDialer, tlsConfig *tls.Config) (any, error) {
	return fetchJSONWithTLSConfigStatus(ctx, config, resolve, dial, tlsConfig, nil)
}

func fetchJSONWithTLSConfigStatus(ctx context.Context, config HTTPConfig, resolve ipResolver, dial contextDialer, tlsConfig *tls.Config, status *int) (any, error) {
	endpoint, err := validateEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if resolve == nil || dial == nil {
		return nil, errors.New("HTTP client dependencies are unavailable")
	}
	requestURL := *endpoint
	query := requestURL.Query()
	for key, values := range config.Query {
		query[key] = append([]string(nil), values...)
	}
	if err := applyAuth(query, config.Auth); err != nil {
		return nil, err
	}
	requestURL.RawQuery = query.Encode()

	requestCtx, cancel := context.WithTimeout(ctx, HTTPTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, errors.New("invalid HTTP request")
	}
	if config.Auth.Mode == AuthBearer {
		request.Header.Set("Authorization", "Bearer "+config.Auth.Token)
	} else if config.Auth.Mode == AuthAPIKeyHeader {
		request.Header.Set(config.Auth.HeaderName, config.Auth.Key)
	}

	transport, err := secureTransport(resolve, dial, tlsConfig)
	if err != nil {
		return nil, err
	}
	client := secureClient(transport)
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("HTTP request failed")
	}
	defer response.Body.Close()
	if status != nil {
		*status = response.StatusCode
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP endpoint returned status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, HTTPMaxResponseBytes+1))
	if err != nil {
		return nil, errors.New("HTTP response could not be read")
	}
	if len(body) > HTTPMaxResponseBytes {
		return nil, errors.New("HTTP response exceeds size limit")
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	var result any
	if err := decoder.Decode(&result); err != nil {
		return nil, errors.New("HTTP response is not valid JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, errors.New("HTTP response must contain exactly one JSON value")
	}
	return result, nil
}

// secureTransport is shared by provider JSON and image fetches. In particular,
// DNS answers are all checked before dialing a pinned numeric address.
func secureTransport(resolve ipResolver, dial contextDialer, tlsConfig *tls.Config) (*http.Transport, error) {
	if resolve == nil || dial == nil {
		return nil, errors.New("HTTP client dependencies are unavailable")
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	} else {
		tlsConfig = tlsConfig.Clone()
		if tlsConfig.MinVersion < tls.VersionTLS12 {
			tlsConfig.MinVersion = tls.VersionTLS12
		}
	}
	// The test seam can supply trusted roots, but can never weaken certificate checks.
	tlsConfig.InsecureSkipVerify = false
	return &http.Transport{
		Proxy:                 nil,
		DisableKeepAlives:     true,
		TLSHandshakeTimeout:   HTTPTimeout,
		ResponseHeaderTimeout: HTTPTimeout,
		TLSClientConfig:       tlsConfig,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, splitErr := net.SplitHostPort(address)
			if splitErr != nil {
				return nil, errors.New("connection failed")
			}
			ips, lookupErr := resolve(ctx, host)
			if lookupErr != nil || len(ips) == 0 {
				return nil, errors.New("connection failed")
			}
			for _, ip := range ips {
				if !isPublicIP(ip) {
					return nil, errors.New("connection failed")
				}
			}
			return dial(ctx, network, net.JoinHostPort(ips[0].Unmap().String(), port))
		},
	}, nil
}

func secureClient(transport *http.Transport) *http.Client {
	return &http.Client{
		Transport: transport,
		Timeout:   HTTPTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func validateEndpoint(raw string) (*url.URL, error) {
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("endpoint must be a valid HTTPS URL without userinfo or fragment")
	}
	host := strings.TrimSuffix(strings.ToLower(endpoint.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, errors.New("endpoint host must be publicly routable")
	}
	if port := endpoint.Port(); port != "" {
		portNumber, parseErr := strconv.Atoi(port)
		if parseErr != nil || portNumber < 1 || portNumber > 65535 {
			return nil, errors.New("endpoint port is invalid")
		}
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil && !isPublicIP(ip) {
		return nil, errors.New("endpoint host must be publicly routable")
	}
	if endpoint.Port() == "" {
		endpoint.Host = net.JoinHostPort(endpoint.Hostname(), "443")
	}
	return endpoint, nil
}

func applyAuth(query url.Values, auth AuthConfig) error {
	switch auth.Mode {
	case "", AuthNone:
		return nil
	case AuthBearer:
		if strings.TrimSpace(auth.Token) == "" || strings.ContainsAny(auth.Token, "\r\n") {
			return errors.New("bearer token is required")
		}
	case AuthAPIKeyHeader:
		if strings.TrimSpace(auth.Key) == "" || strings.ContainsAny(auth.Key, "\r\n") || !validHeaderName(auth.HeaderName) {
			return errors.New("API key header configuration is invalid")
		}
	case AuthAPIKeyQuery:
		if strings.TrimSpace(auth.Key) == "" || strings.TrimSpace(auth.QueryName) == "" {
			return errors.New("API key query configuration is invalid")
		}
		query.Set(auth.QueryName, auth.Key)
	default:
		return errors.New("authentication mode is invalid")
	}
	return nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return false
		}
	}
	return true
}

func defaultResolve(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip}, nil
	}
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

func isPublicIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	if ip.Is4() {
		for _, prefix := range blockedIPv4Prefixes {
			if prefix.Contains(ip) {
				return false
			}
		}
		return true
	}
	if !netip.MustParsePrefix("2000::/3").Contains(ip) {
		return false
	}
	for _, prefix := range blockedIPv6Prefixes {
		if prefix.Contains(ip) {
			return false
		}
	}
	return true
}
