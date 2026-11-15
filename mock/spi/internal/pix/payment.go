package pix

import (
	"context"
	"errors"
	"time"
)

// Payment status. A payment waits for the receiving participant, then settles or is rejected.
const (
	StatusPending  = "PENDING"
	StatusSettled  = "SETTLED"
	StatusRejected = "REJECTED"
)

// ErrPaymentNotFound is returned when there is no payment with the EndToEndId.
var ErrPaymentNotFound = errors.New("payment not found")

// Kinds of Payment.
const (
	KindPayment = "PAYMENT" // a credit transfer (pacs.008)
	KindReturn  = "RETURN"  // money going back to the payer of an earlier payment (pacs.004)
)

// Payment is a transfer of money accepted by the SPI: a credit transfer or a return. Both go through
// the same cycle, so they are stored and settled the same way. "Payer" is whoever sends the money and
// "Payee" whoever receives it; in a return that is the original receiver sending it back to the original payer.
type Payment struct {
	// ID identifies the transfer: the EndToEndId of a payment, the RtrId (idOperacao) of a return.
	ID   string
	Kind string
	// EndToEndID is the EndToEndId of the payment. A return carries that of the payment it returns, so
	// there can be several returns (partial ones) with the same EndToEndID.
	EndToEndID string
	Status     string
	// ReasonCode and ReasonDetail are set when the payment is rejected.
	ReasonCode   string
	ReasonDetail string

	AmountCents int64
	Purpose     string
	Initiation  string // MANU or DICT
	Info        string
	// ReturnReason is the reason code of a return (MD06...).
	ReturnReason string

	PayerISPB        string
	PayerName        string
	PayerTaxID       string
	PayerBranch      string
	PayerAccount     string
	PayerAccountType string

	PayeeISPB        string
	PayeeTaxID       string
	PayeeBranch      string
	PayeeAccount     string
	PayeeAccountType string
	PayeeKey         string

	Priority   string // PAGPRI
	AcceptedAt time.Time
	CreatedAt  time.Time
	SettledAt  time.Time // zero until settled
}

// PaymentStore keeps the payments.
type PaymentStore interface {
	// CreatePayment stores a new payment. When the ID already exists it stores nothing
	// and returns the existing payment with created == false.
	CreatePayment(ctx context.Context, p Payment) (existing Payment, created bool, err error)
	// GetPayment returns the payment or return with the ID, or ErrPaymentNotFound.
	GetPayment(ctx context.Context, id string) (Payment, error)
	// ReturnsOf returns the returns made for the payment with the EndToEndId, whatever their status.
	ReturnsOf(ctx context.Context, endToEndID string) ([]Payment, error)
	// PendingBefore returns the IDs of the PENDING payments created at or before t.
	PendingBefore(ctx context.Context, t time.Time) ([]string, error)
	// FinalizePayment moves a PENDING payment to SETTLED or REJECTED. When the payment is no
	// longer pending it changes nothing and returns changed == false with its current state.
	FinalizePayment(ctx context.Context, id, status, reasonCode, reasonDetail string, at time.Time) (p Payment, changed bool, err error)
}
