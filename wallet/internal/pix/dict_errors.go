package pix

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Use errors.Is with these on the errors of DictClient and SPIClient.
var (
	ErrNotFound             = errors.New("dict: key not found")
	ErrConflict             = errors.New("dict: key already registered")
	ErrBookTransfer         = errors.New("dict: key held by the requesting participant")
	ErrRequestIDAlreadyUsed = errors.New("dict: request id already used with other data")
	ErrBadRequest           = errors.New("pix: invalid request")
	ErrForbidden            = errors.New("pix: forbidden")
	ErrRateLimited          = errors.New("pix: too many requests")
	// ErrUnavailable covers 5xx and network failures; for a write, the outcome is unknown.
	ErrUnavailable = errors.New("pix: unavailable")
)

// APIError is a problem+xml answer of the DICT.
type APIError struct {
	Status        int
	Kind          string // last segment of the problem type, such as "NotFound"
	Title         string
	Detail        string
	CorrelationID string
	Violations    []Violation
	RetryAfter    time.Duration
}

type Violation struct {
	Reason   string
	Value    string
	Property string
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dict: %d %s", e.Status, e.Kind)
	if e.Detail != "" {
		b.WriteString(": " + e.Detail)
	}
	for _, v := range e.Violations {
		fmt.Fprintf(&b, " [%s: %s]", v.Property, v.Reason)
	}
	if e.CorrelationID != "" {
		b.WriteString(" (correlation " + e.CorrelationID + ")")
	}
	return b.String()
}

func (e *APIError) Is(target error) bool {
	switch target {
	case ErrBookTransfer:
		return e.Kind == "EntryCannotBeQueriedForBookTransfer"
	case ErrRequestIDAlreadyUsed:
		return e.Kind == "RequestIdAlreadyUsed"
	case ErrNotFound:
		return e.Status == 404
	case ErrConflict:
		return e.Status == 409
	case ErrForbidden:
		return e.Status == 403
	case ErrRateLimited:
		return e.Status == 429
	case ErrUnavailable:
		return e.Status >= 500
	case ErrBadRequest:
		return e.Status == 400
	}
	return false
}

// Retryable reports whether the same call can work later: rate limit or unavailability.
func Retryable(err error) bool {
	return errors.Is(err, ErrRateLimited) || errors.Is(err, ErrUnavailable)
}
