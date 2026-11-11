package dict

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// NewEVP gera uma chave aleatória (EVP): um UUID v4 em minúsculas.
func NewEVP() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read não retorna erro (Go 1.24+)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s", h[0:8], h[8:12], h[12:16], h[16:20], h[20:])
}
