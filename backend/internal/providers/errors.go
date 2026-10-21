package providers

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
)

type safeError string

func (e safeError) Error() string         { return string(e) }
func (e safeError) SyncErrorCode() string { return string(e) }

// SafeError accepts operational codes only, never provider content.
func SafeError(code string) error {
	if !allowedSyncCode(code) {
		code = "provider_import_error"
	}
	return safeError(code)
}

func HTTPError(status int) error { return SafeError("provider_http_" + strconv.Itoa(status)) }

func allowedSyncCode(code string) bool {
	switch code {
	case "provider_import_error", "provider_cancelled", "provider_timeout", "provider_transport_error", "provider_decode_error", "provider_pagination_error", "provider_city_mismatch", "provider_origin_error", "provider_empty_result", "provider_response_too_large", "provider_persistence_error", "provider_finalization_error", "provider_coverage_drop", "timepad_normalization_empty":
		return true
	}
	if len(code) == len("provider_http_000") && strings.HasPrefix(code, "provider_http_") {
		n, err := strconv.Atoi(strings.TrimPrefix(code, "provider_http_"))
		return err == nil && n >= 100 && n <= 599
	}
	return false
}

func IsBlockingError(err error) bool {
	switch SyncErrorCode(err) {
	case "provider_http_401", "provider_http_403", "provider_http_429":
		return true
	}
	return false
}

// SyncErrorCode never returns caller-controlled diagnostic text.
func SyncErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "provider_cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "provider_timeout"
	}
	var coded interface{ SyncErrorCode() string }
	if errors.As(err, &coded) && allowedSyncCode(coded.SyncErrorCode()) {
		return coded.SyncErrorCode()
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "provider_timeout"
	}
	return "provider_import_error"
}
