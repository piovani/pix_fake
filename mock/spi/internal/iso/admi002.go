package iso

import (
	"encoding/xml"
	"fmt"
)

// Admi002ID is the identification of the error notification message.
const (
	Admi002ID = "admi.002.spi.1.5"
	nsAdmi002 = "https://www.bcb.gov.br/pi/admi.002/1.5"
)

// Admi002 is the SPI's notification that a message it received could not be processed.
type Admi002 struct {
	XMLName  xml.Name `xml:"Envelope"`
	Xmlns    string   `xml:"xmlns,attr"`
	AppHdr   AppHdr   `xml:"AppHdr"`
	Document struct {
		Admi struct {
			RltdRef struct {
				Ref string `xml:"Ref"`
			} `xml:"RltdRef"`
			Rsn struct {
				RejectingPartyReason string `xml:"RjctgPtyRsn"`
				RejectionTime        string `xml:"RjctnDtTm,omitempty"`
				ErrorLocation        string `xml:"ErrLctn,omitempty"`
				Description          string `xml:"RsnDesc,omitempty"`
			} `xml:"Rsn"`
		} `xml:"admi.002.001.01"`
	} `xml:"Document"`
}

// NewAdmi002 builds the notification. ref identifies the rejected message (its PI-ResourceId, at most
// 33 characters); reason is a short title (at most 35), location and description are free text.
func NewAdmi002(from, to, msgID, createdAt, ref, reason, location, description string) *Admi002 {
	m := &Admi002{Xmlns: nsAdmi002}
	m.AppHdr = AppHdr{Fr: NewParty(from), To: NewParty(to), BizMsgIdr: msgID, MsgDefIdr: Admi002ID, CreDt: createdAt}
	a := &m.Document.Admi
	a.RltdRef.Ref = clip(ref, 33)
	a.Rsn.RejectingPartyReason = clip(reason, 35)
	a.Rsn.RejectionTime = createdAt
	a.Rsn.ErrorLocation = clip(location, 350)
	a.Rsn.Description = clip(description, 350)
	return m
}

// ParseAdmi002 reads an admi.002 message.
func ParseAdmi002(body []byte) (*Admi002, error) {
	var m Admi002
	if err := xml.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("invalid admi.002: %w", err)
	}
	return &m, nil
}

// Ref returns the reference to the rejected message.
func (m *Admi002) Ref() string { return m.Document.Admi.RltdRef.Ref }

// Reason returns the short reason title.
func (m *Admi002) Reason() string { return m.Document.Admi.Rsn.RejectingPartyReason }

// Marshal returns the message as XML with the declaration.
func (m *Admi002) Marshal() ([]byte, error) { return marshal(m) }

func clip(s string, max int) string {
	if r := []rune(s); len(r) > max {
		return string(r[:max])
	}
	return s
}
