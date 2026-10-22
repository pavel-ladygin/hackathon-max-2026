package timepad

import (
	"context"
	"errors"
	"fmt"
	"net"
)

type requestError struct {
	code  string
	cause error
}

func (e *requestError) Error() string {
	if e.code == "timepad_http_429" {
		return "timepad rate limit exceeded (status 429)"
	}
	return e.code
}
func (e *requestError) Unwrap() error         { return e.cause }
func (e *requestError) SyncErrorCode() string { return e.code }
func requestFailure(err error) error {
	code := "timepad_transport_error"
	var n net.Error
	if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &n) && n.Timeout()) {
		code = "timepad_timeout"
	}
	return &requestError{code, err}
}
func httpFailure(status int) error { return &requestError{fmt.Sprintf("timepad_http_%d", status), nil} }
func decodeFailure(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return requestFailure(err)
	}
	return &requestError{"timepad_decode_error", err}
}
