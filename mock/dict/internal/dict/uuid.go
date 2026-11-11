package dict

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// NewEVP generates a random key (EVP): a lowercase UUID v4.
func NewEVP() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error (Go 1.24+)
	return formatUUID(b)
}

// deterministicUUID always derives the same UUID v4 from s.
func deterministicUUID(s string) string {
	sum := sha256.Sum256([]byte(s))
	return formatUUID([16]byte(sum[:16]))
}

func formatUUID(b [16]byte) string {
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:])
}
