package dict

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// The reconciliation of the spec (tag Reconciliation): every entry has a content identifier (CID), and the XOR of the CIDs
// of a participant's entries of one key type is the sync verifier (VSync) both sides compare.

var cidRe = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

// ZeroVSync is the VSync of no CIDs.
const ZeroVSync = "0000000000000000000000000000000000000000000000000000000000000000"

// CID is hmacSha256(requestIdBytes, keyType&key&ownerTaxIdNumber&ownerName&ownerTradeName&participant&branch&accountNumber&accountType),
// in lowercase hexadecimal. requestIdBytes are the 16 bytes of the RequestId that created the entry.
func CID(e Entry, requestID string) (string, error) {
	raw, err := hex.DecodeString(strings.ReplaceAll(requestID, "-", ""))
	if err != nil || len(raw) != 16 {
		return "", fmt.Errorf("dict: request id %q is not a UUID", requestID)
	}
	return cidOf(e, raw), nil
}

func cidOf(e Entry, requestIDBytes []byte) string {
	attrs := strings.Join([]string{
		string(e.KeyType), e.Key, e.Owner.TaxID, e.Owner.Name, e.Owner.TradeName,
		e.Account.Participant, e.Account.Branch, e.Account.AccountNumber, string(e.Account.Type),
	}, "&")
	mac := hmac.New(sha256.New, requestIDBytes)
	mac.Write([]byte(attrs))
	return hex.EncodeToString(mac.Sum(nil))
}

// VSync folds CIDs with XOR; adding a CID twice takes it out again, which is how a removal is applied.
type VSync [sha256.Size]byte

func (v *VSync) Add(cid string) error {
	raw, err := hex.DecodeString(cid)
	if err != nil || len(raw) != len(v) {
		return fmt.Errorf("dict: %q is not a CID", cid)
	}
	for i := range v {
		v[i] ^= raw[i]
	}
	return nil
}

func (v VSync) String() string { return hex.EncodeToString(v[:]) }

type CIDEventType string

const (
	CIDAdded   CIDEventType = "ADDED"
	CIDRemoved CIDEventType = "REMOVED"
)

// CIDEvent is a change to the set of CIDs of a participant and key type: creating an entry adds its CID, removing it
// removes it, and updating it does both.
type CIDEvent struct {
	Type      CIDEventType
	CID       string
	Timestamp time.Time
}

type SyncResult string

const (
	SyncOK  SyncResult = "OK"
	SyncNOK SyncResult = "NOK"
)

type SyncVerification struct {
	ID          int64
	Participant string
	KeyType     KeyType
	Verifier    string // the participant's
	DictVSync   string // the DICT's, kept for whoever looks at the database; not in the answer
	Result      SyncResult
}

type FileStatus string

const (
	FileRequested FileStatus = "REQUESTED"
	FileAvailable FileStatus = "AVAILABLE"
)

// CIDFile is the list of the CIDs of a participant and key type, one per line.
type CIDFile struct {
	ID           int64
	Status       FileStatus
	Participant  string
	KeyType      KeyType
	RequestTime  time.Time
	CreationTime time.Time
	Content      []byte
	Sha256       string
}
