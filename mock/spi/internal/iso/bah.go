// Package iso holds the XML types of the ISO 20022 messages the SPI mock reads and writes
// (the Brazilian SPI profile, catalog package spi.5.13.2), and their parsing and formatting.
package iso

import (
	"encoding/xml"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Namespaces and message identifiers of the schema versions the mock supports.
const (
	Pacs008ID = "pacs.008.spi.1.16"
	Pacs002ID = "pacs.002.spi.1.18"

	nsPacs008 = "https://www.bcb.gov.br/pi/pacs.008/1.16"
	nsPacs002 = "https://www.bcb.gov.br/pi/pacs.002/1.18"

	// TimeLayout is the UTC date and time format of the messages: YYYY-MM-DD'T'hh:mm:ss.sss'Z'.
	TimeLayout = "2006-01-02T15:04:05.000Z"
	// DateLayout is the accounting date format.
	DateLayout = "2006-01-02"
)

// FormatTime formats t in UTC as the messages require.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeLayout) }

// ParseTime reads a date and time in the format of the messages.
func ParseTime(s string) (time.Time, error) {
	t, err := time.Parse(TimeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date and time %q", s)
	}
	return t, nil
}

// Party identifies a participant in the business application header (FIId/FinInstnId/Othr/Id).
type Party struct {
	FIId struct {
		FinInstnId struct {
			Othr struct {
				ID string `xml:"Id"`
			} `xml:"Othr"`
		} `xml:"FinInstnId"`
	} `xml:"FIId"`
}

// ISPB returns the participant code.
func (p Party) ISPB() string { return p.FIId.FinInstnId.Othr.ID }

// NewParty builds a Party for an ISPB.
func NewParty(ispb string) Party {
	var p Party
	p.FIId.FinInstnId.Othr.ID = ispb
	return p
}

// AppHdr is the business application header of every message.
type AppHdr struct {
	Fr        Party    `xml:"Fr"`
	To        Party    `xml:"To"`
	BizMsgIdr string   `xml:"BizMsgIdr"`
	MsgDefIdr string   `xml:"MsgDefIdr"`
	CreDt     string   `xml:"CreDt"`
	Sgntr     struct{} `xml:"Sgntr"`
}

// Header reads only the application header of a message, to find out what it is.
func Header(body []byte) (AppHdr, error) {
	var env struct {
		AppHdr AppHdr `xml:"AppHdr"`
	}
	if err := xml.Unmarshal(body, &env); err != nil {
		return AppHdr{}, fmt.Errorf("invalid XML: %w", err)
	}
	if env.AppHdr.MsgDefIdr == "" {
		return AppHdr{}, errors.New("missing AppHdr/MsgDefIdr")
	}
	return env.AppHdr, nil
}

// Family returns the message family of a MsgDefIdr: "pacs.008" for "pacs.008.spi.1.16".
func Family(msgDefIdr string) string {
	parts := strings.SplitN(msgDefIdr, ".", 3)
	if len(parts) < 2 {
		return msgDefIdr
	}
	return parts[0] + "." + parts[1]
}
