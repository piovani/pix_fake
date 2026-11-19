package pix

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

const endToEndAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

var endToEndRe = regexp.MustCompile(`^E[0-9]{8}[0-9]{12}[A-Za-z0-9]{11}$`)

// NewEndToEndID creates a payment identifier: "E" + ISPB (8) + yyyyMMddHHmm in UTC (12) + 11 random characters.
func NewEndToEndID(ispb string, now time.Time) (string, error) {
	if len(ispb) != 8 {
		return "", fmt.Errorf("pix: ISPB must have 8 digits, got %q", ispb)
	}
	suffix, err := randomString(11)
	if err != nil {
		return "", err
	}
	id := "E" + ispb + now.UTC().Format("200601021504") + suffix
	if !endToEndRe.MatchString(id) {
		return "", fmt.Errorf("pix: invalid EndToEndId %q (is the ISPB numeric?)", id)
	}
	return id, nil
}

// NewRequestID creates a UUID v4.
func NewRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("pix: random: %w", err)
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:]), nil
}

func randomString(n int) (string, error) {
	out := make([]byte, 0, n)
	buf := make([]byte, n*2)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("pix: random: %w", err)
		}
		for _, b := range buf {
			// bytes of 248 or more are skipped so every character is equally likely
			if b < 248 && len(out) < n {
				out = append(out, endToEndAlphabet[int(b)%len(endToEndAlphabet)])
			}
		}
	}
	return string(out), nil
}

var messageIDRe = regexp.MustCompile(`^M[0-9A-Z]{8}[A-Za-z0-9]{23}$`)

// NewMessageID creates the identifier of a message: "M" + ISPB of the sender (8) + 23 random characters.
func NewMessageID(ispb string) (string, error) {
	suffix, err := randomString(23)
	if err != nil {
		return "", err
	}
	id := "M" + ispb + suffix
	if !messageIDRe.MatchString(id) {
		return "", fmt.Errorf("pix: invalid MsgId %q (is the ISPB 8 characters of 0-9 A-Z?)", id)
	}
	return id, nil
}
