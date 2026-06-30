package toniecloud

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNotAuthenticated is returned when no credentials and no usable cached token
// are available.
var ErrNotAuthenticated = errors.New("not authenticated: set TONIE_USERNAME and TONIE_PASSWORD (or run `tonys auth login`)")

// APIError represents a non-2xx HTTP response from TonieCloud (or S3). Unlike the
// upstream Python library, which silently swallows failures and returns an empty
// dict, we surface the status, endpoint and server-provided detail so callers
// (and agents) can react.
type APIError struct {
	Method string
	URL    string
	Status int
	// Detail is the server's human-readable message when one could be parsed.
	Detail string
	// Body is the raw response body (truncated) for debugging.
	Body string
}

func (e *APIError) Error() string {
	msg := e.Detail
	if msg == "" {
		msg = e.Body
	}
	msg = strings.TrimSpace(msg)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	if msg == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.URL, e.Status, msg)
}

// IsUnauthorized reports whether err is an APIError with a 401/403 status, which
// usually means the token expired or the credentials are wrong.
func IsUnauthorized(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == 401 || apiErr.Status == 403
	}
	return false
}

// IsNotFound reports whether err is an APIError with a 404 status.
func IsNotFound(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Status == 404
	}
	return false
}
