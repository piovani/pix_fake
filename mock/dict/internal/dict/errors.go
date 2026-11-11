package dict

import "errors"

// Kind identifica o tipo de erro de negócio. O valor é usado como último
// segmento do campo "type" do problem+xml (…/api/v2/error/<Kind>).
type Kind string

const (
	KindBadRequest           Kind = "BadRequest"
	KindEntryInvalid         Kind = "EntryInvalid"
	KindAccountTypeInvalid   Kind = "AccountTypeInvalid"
	KindRequestIDAlreadyUsed Kind = "RequestIdAlreadyUsed"
	KindForbidden            Kind = "Forbidden"
	KindNotFound             Kind = "NotFound"
	KindConflict             Kind = "ResourceConflict"
)

// Violation descreve um campo inválido (elemento <violation> do problem+xml).
type Violation struct {
	Reason   string
	Value    string
	Property string
}

// Error é um erro de negócio que a camada HTTP traduz para problem+xml.
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

// Erros sentinela devolvidos pelo Repository.
var (
	ErrNotFound      = errors.New("entry not found")
	ErrKeyExists     = errors.New("key already exists")
	ErrRequestIDUsed = errors.New("request id already used")
)

type violations []Violation

func (v *violations) add(property, value, reason string) {
	*v = append(*v, Violation{Reason: reason, Value: value, Property: property})
}
