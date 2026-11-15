package pix

import (
	"context"
	"time"
)

// PaymentFilter selects the payments (and returns) to list. Empty fields select everything.
type PaymentFilter struct {
	Status      string // PENDING, SETTLED or REJECTED
	Kind        string // PAYMENT or RETURN
	Participant string // the payer's or the receiver's participant
	Limit       int
}

// MessageFilter selects the stored messages to list.
type MessageFilter struct {
	Participant string // who sent it (inbound) or who it is for (outbound)
	Direction   string // "in" (sent by a participant), "out" (queued for a participant) or empty
	Limit       int
}

// StoredMessage is a message as the SPI kept it.
type StoredMessage struct {
	Seq         int64     `json:"seq"`
	ResourceID  string    `json:"resource_id"`
	Participant string    `json:"participant"`
	Direction   string    `json:"direction"`
	CreatedAt   time.Time `json:"created_at"`
	Body        string    `json:"body"`
}

// Inspector is what the control API needs to look at the state of the mock and to clear it.
type Inspector interface {
	// ListPayments returns the newest payments first.
	ListPayments(ctx context.Context, f PaymentFilter) ([]Payment, error)
	// ListMessages returns the newest messages first.
	ListMessages(ctx context.Context, f MessageFilter) ([]StoredMessage, error)
	// ListAccounts returns the accounts ordered by participant and number.
	ListAccounts(ctx context.Context) ([]Account, error)
	// Reset deletes messages, reading positions, payments and reported transactions. Accounts stay.
	Reset(ctx context.Context) error
}

// DefaultListLimit and MaxListLimit bound the lists of the control API.
const (
	DefaultListLimit = 50
	MaxListLimit     = 500
)

// ClampLimit applies the default and the maximum to a requested limit.
func ClampLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultListLimit
	case n > MaxListLimit:
		return MaxListLimit
	}
	return n
}
