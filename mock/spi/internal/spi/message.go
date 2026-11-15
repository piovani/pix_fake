// Package spi is the core of the SPI mock: the message exchange between participants
// and the Bacen instant payment system (ICOM interface).
package spi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

// MaxMessages is the most messages the API accepts or returns in one request (multipart).
const MaxMessages = 10

// ErrStreamGone means the stream token is malformed or older than what the participant already acknowledged.
var ErrStreamGone = errors.New("stream gone")

// Message is one stored ISO 20022 message. The body is kept exactly as received.
type Message struct {
	Seq        int64 // order of delivery; set by the store
	ResourceID string
	ISPB       string // sender for inbound messages, recipient for outbound ones
	Body       []byte
	CreatedAt  time.Time
}

// ValidISPB reports whether s has the ISPB format: 8 characters in [0-9A-Z].
func ValidISPB(s string) bool {
	if len(s) != 8 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// NewResourceID returns a PI-ResourceId: 24 random bytes in Base64, 32 characters.
func NewResourceID() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.StdEncoding.EncodeToString(b[:])
}
