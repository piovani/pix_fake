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

// Payment is a credit transfer accepted by the SPI, identified by its EndToEndId.
type Payment struct {
	EndToEndID string
	Status     string
	// ReasonCode and ReasonDetail are set when the payment is rejected.
	ReasonCode   string
	ReasonDetail string

	AmountCents int64
	Purpose     string
	Initiation  string // MANU or DICT
	Info        string

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
	// CreatePayment stores a new payment. When the EndToEndId already exists it stores nothing
	// and returns the existing payment with created == false.
	CreatePayment(ctx context.Context, p Payment) (existing Payment, created bool, err error)
	// GetPayment returns the payment or ErrPaymentNotFound.
	GetPayment(ctx context.Context, endToEndID string) (Payment, error)
	// FinalizePayment moves a PENDING payment to SETTLED or REJECTED. When the payment is no
	// longer pending it changes nothing and returns changed == false with its current state.
	FinalizePayment(ctx context.Context, endToEndID, status, reasonCode, reasonDetail string, at time.Time) (p Payment, changed bool, err error)
}
