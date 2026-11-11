package httpapi

import (
	"encoding/xml"
	"net/http"

	"pixfake/mock/dict/internal/dict"
)

const problemTypeBase = "https://dict.pi.rsfn.net.br/api/v2/error/"

// kindServiceUnavailable does not exist as a dict.Kind: unexpected errors become 503.
const kindServiceUnavailable dict.Kind = "ServiceUnavailable"

var problems = map[dict.Kind]struct {
	status int
	title  string
}{
	dict.KindBadRequest:           {http.StatusBadRequest, "Bad Request"},
	dict.KindEntryInvalid:         {http.StatusBadRequest, "Entry is invalid"},
	dict.KindBookTransfer:         {http.StatusBadRequest, "Entry cannot be queried for a book transfer"},
	dict.KindAccountTypeInvalid:   {http.StatusBadRequest, "Account type is invalid"},
	dict.KindRequestIDAlreadyUsed: {http.StatusBadRequest, "RequestId already used"},
	dict.KindForbidden:            {http.StatusForbidden, "Forbidden"},
	dict.KindNotFound:             {http.StatusNotFound, "Not found"},
	dict.KindConflict:             {http.StatusConflict, "Conflict"},
	kindServiceUnavailable:        {http.StatusServiceUnavailable, "Service Unavailable"},
}

// problemXML follows RFC 7807 in XML, as in the spec's Problem schema.
type problemXML struct {
	XMLName       xml.Name       `xml:"urn:ietf:rfc:7807 problem"`
	Type          string         `xml:"type"`
	Title         string         `xml:"title"`
	Status        int            `xml:"status"`
	Detail        string         `xml:"detail,omitempty"`
	CorrelationID string         `xml:"correlationId,omitempty"`
	Violations    *violationsXML `xml:"violations,omitempty"`
}

type violationsXML struct {
	Violation []violationXML `xml:"violation"`
}

type violationXML struct {
	Reason   string `xml:"reason"`
	Value    string `xml:"value,omitempty"`
	Property string `xml:"property,omitempty"`
}

func newProblem(e *dict.Error, correlationID string) (int, problemXML) {
	info := problems[e.Kind]
	p := problemXML{
		Type:          problemTypeBase + string(e.Kind),
		Title:         info.title,
		Status:        info.status,
		Detail:        e.Detail,
		CorrelationID: correlationID,
	}
	if len(e.Violations) > 0 {
		p.Violations = &violationsXML{}
		for _, v := range e.Violations {
			p.Violations.Violation = append(p.Violations.Violation,
				violationXML{Reason: v.Reason, Value: v.Value, Property: v.Property})
		}
	}
	return info.status, p
}
