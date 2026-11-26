package pix

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// ZeroVSync is the VSync of no CIDs.
const ZeroVSync = "0000000000000000000000000000000000000000000000000000000000000000"

// CID is the content identifier the DICT gives an entry (DICT API, Reconciliation): hmacSha256 keyed by the 16 bytes of
// the RequestId that created it, over keyType&key&ownerTaxIdNumber&ownerName&ownerTradeName&participant&branch&accountNumber&accountType,
// in lowercase hexadecimal. The wallet computes it from its own records to compare them with the DICT's.
func CID(e Entry, requestID string) (string, error) {
	key, err := hex.DecodeString(strings.ReplaceAll(requestID, "-", ""))
	if err != nil || len(key) != 16 {
		return "", fmt.Errorf("pix: request id %q is not a UUID", requestID)
	}
	attrs := strings.Join([]string{
		string(e.KeyType), e.Key, e.Owner.TaxIDNumber, e.Owner.Name, e.Owner.TradeName,
		e.Account.Participant, e.Account.Branch, e.Account.AccountNumber, string(e.Account.AccountType),
	}, "&")
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(attrs))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

// VSync is the XOR of a set of CIDs, what a sync verification compares.
type VSync [sha256.Size]byte

func (v *VSync) Add(cid string) error {
	raw, err := hex.DecodeString(cid)
	if err != nil || len(raw) != len(v) {
		return fmt.Errorf("pix: %q is not a CID", cid)
	}
	for i := range v {
		v[i] ^= raw[i]
	}
	return nil
}

func (v VSync) String() string { return hex.EncodeToString(v[:]) }
