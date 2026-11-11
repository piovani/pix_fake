package dict

import "errors"

// Kind identifies the kind of business error. The value is used as the last
// segment of the "type" field of the problem+xml (…/api/v2/error/<Kind>).
type Kind string

const (
	KindBadRequest   Kind = "BadRequest"
	KindEntryInvalid Kind = "EntryInvalid"
	// KindBookTransfer: the entry is held by the participant that is querying it.
	KindBookTransfer         Kind = "EntryCannotBeQueriedForBookTransfer"
	KindAccountTypeInvalid   Kind = "AccountTypeInvalid"
	KindRequestIDAlreadyUsed Kind = "RequestIdAlreadyUsed"
	KindForbidden            Kind = "Forbidden"
	KindNotFound             Kind = "NotFound"
	KindConflict             Kind = "ResourceConflict"
)

// Violation describes an invalid field (the <violation> element of the problem+xml).
type Violation struct {
	Reason   string
	Value    string
	Property string
}

// Error is a business error that the HTTP layer translates into problem+xml.
type Error struct {
	Kind       Kind
	Detail     string
	Violations []Violation
}

func (e *Error) Error() string { return string(e.Kind) + ": " + e.Detail }

func newError(kind Kind, detail string) *Error {
	return &Error{Kind: kind, Detail: detail}
}

func invalid(kind Kind, detail string, v violations) *Error {
	return &Error{Kind: kind, Detail: detail, Violations: v}
}

// Sentinel errors returned by the Repository.
var (
	ErrNotFound      = errors.New("entry not found")
	ErrKeyExists     = errors.New("key already exists")
	ErrRequestIDUsed = errors.New("request id already used")
)

type violations []Violation

func (v *violations) add(property, value, reason string) {
	*v = append(*v, Violation{Reason: reason, Value: value, Property: property})
}
