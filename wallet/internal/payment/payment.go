package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"unicode/utf8"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

// Errors of the module. Callers compare them with errors.Is. What the ledger refuses (ledger.ErrInsufficientFunds,
// ledger.ErrAccountNotFound) and what the DICT and the SPI refuse while being unavailable (pix.ErrUnavailable,
// pix.ErrRateLimited) pass through unchanged.
var (
	ErrInvalid            = errors.New("payment: invalid request")
	ErrKeyNotFound        = errors.New("payment: key not found")
	ErrResolutionNotFound = errors.New("payment: resolution not found or expired")
	ErrResolutionConflict = errors.New("payment: resolution already used with another amount or message")
	ErrPaymentNotFound    = errors.New("payment: payment not found")
	ErrKeyNotRecorded     = errors.New("payment: the DICT says the key is the wallet's but the wallet has no record of it")
)

type (
	Status    string
	Direction string
	Type      string
)

const (
	Pending  Status = "PENDING"
	Settled  Status = "SETTLED"
	Rejected Status = "REJECTED"

	Outbound Direction = "OUTBOUND"
	Inbound  Direction = "INBOUND"

	TypePix          Type = "PIX"
	TypeBookTransfer Type = "BOOK_TRANSFER"
	TypeReturn       Type = "RETURN"
)

const (
	// ResolutionTTL is how long a resolution can be paid after the lookup.
	ResolutionTTL = 5 * time.Minute
	// MaxMessage is the longest free text between users, as in the pacs.008.
	MaxMessage = 140
	// resubmitAfter is how old an unsent payment must be before the worker sends it, so it does not race with the call
	// that created it.
	resubmitAfter = 10 * time.Second
)

// Payee is the other side of a payment.
type Payee struct {
	Name          string
	TaxID         string
	Participant   string // ISPB of the institution that holds the account
	Branch        string
	AccountNumber string
	AccountType   pix.AccountType
	Key           string
}

// Resolution is the lookup of a key before paying it. Its EndToEndId was created before the lookup and is the one
// the payment carries.
type Resolution struct {
	ID             string
	AccountID      string
	Key            string
	EndToEndID     string
	Payee          Payee
	ExpiresAt      time.Time
	BookTransfer   bool
	PayeeAccountID string
}

// Payment is a payment of an account, sent or received.
type Payment struct {
	ID            string
	AccountID     string
	Direction     Direction
	Type          Type
	Status        Status
	AmountCents   int64
	EndToEndID    string
	ResolutionID  string
	ReservationID string
	Counterparty  Payee
	Message       string
	RejectCode    string
	RejectReason  string
	ResourceID    string    // the PI-ResourceId the SPI gave the order; empty until the SPI accepted it
	SubmittedAt   time.Time // zero until the SPI accepted the order
	CreatedAt     time.Time
	FinishedAt    time.Time // zero while pending
	// report of a book transfer to the SPI: "", ReportSent, ReportAccepted or ReportRejected
	ReportStatus string
	ReportSentAt time.Time
	ReportReason string
}

const (
	ReportSent     = "SENT"
	ReportAccepted = "ACCEPTED"
	ReportRejected = "REJECTED"
)

// Submitted reports whether the SPI accepted the order for processing.
func (p Payment) Submitted() bool { return !p.SubmittedAt.IsZero() }

// Accounts is what the module needs from the ledger.
type Accounts interface {
	Account(ctx context.Context, id string) (ledger.Account, error)
	Reserve(ctx context.Context, accountID string, cents int64, reference string) (ledger.Reservation, error)
	Settle(ctx context.Context, reservationID string) error
	Release(ctx context.Context, reservationID string) error
	Transfer(ctx context.Context, fromAccountID, toAccountID string, cents int64, reference string) error
}

type Keys interface {
	AccountOf(ctx context.Context, key string) (accountID string, found bool, err error)
}

// Directory is what the module needs from the DICT.
type Directory interface {
	GetEntry(ctx context.Context, p pix.GetEntryParams) (pix.Entry, error)
}

// Bank is what the module needs from the SPI to send payments.
type Bank interface {
	Send(ctx context.Context, bodies ...[]byte) ([]string, error)
}

// Store keeps resolutions and payments.
type Store interface {
	SaveResolution(ctx context.Context, r Resolution) (Resolution, error)
	Resolution(ctx context.Context, id string) (Resolution, bool, error)

	// Insert returns the existing payment, with created false, when the resolution or the EndToEndId and direction already have one.
	Insert(ctx context.Context, p Payment) (stored Payment, created bool, err error)
	Discard(ctx context.Context, id string) error
	ByID(ctx context.Context, id string) (Payment, bool, error)
	ByEndToEnd(ctx context.Context, endToEndID string, direction Direction) (Payment, bool, error)
	ByResolution(ctx context.Context, resolutionID string) (Payment, bool, error)
	ByResource(ctx context.Context, resourceID string) (Payment, bool, error)
	// MarkSubmitted records that the SPI accepted the order.
	MarkSubmitted(ctx context.Context, id, resourceID string, at time.Time) error
	// Close moves a pending payment to Settled or Rejected. It reports false when the payment was not pending.
	Close(ctx context.Context, id string, status Status, rejectCode, rejectReason string, at time.Time) (bool, error)
	// Unsent returns pending payments created before the given time that the SPI has not accepted.
	Unsent(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	List(ctx context.Context, q Query) ([]Payment, error)

	PendingBookTransfers(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	UnreportedBookTransfers(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	// SetReport never changes a report already accepted.
	SetReport(ctx context.Context, id, status, reason string, at time.Time) error
}

// Query is a page of payments of an account, newest first. After is the position of the last payment of the previous page.
type Query struct {
	AccountID string
	Statuses  []Status
	After     *Cursor
	Limit     int
}

// Cursor is the position of a payment in the listing.
type Cursor struct {
	CreatedAt time.Time
	ID        string
}

type Service struct {
	accounts Accounts
	keys     Keys
	dict     Directory
	bank     Bank
	store    Store
	ispb     string
	now      func() time.Time
}

func New(accounts Accounts, keys Keys, dict Directory, bank Bank, store Store, ispb string) *Service {
	return &Service{accounts: accounts, keys: keys, dict: dict, bank: bank, store: store, ispb: ispb, now: time.Now}
}

// ResolveKey creates the EndToEndId before the lookup, as the DICT requires. A key of another customer of the wallet is
// found in the wallet's own records and never looked up in the DICT.
func (s *Service) ResolveKey(ctx context.Context, accountID, key string) (Resolution, error) {
	if accountID == "" || key == "" {
		return Resolution{}, fmt.Errorf("%w: account and key are required", ErrInvalid)
	}
	acct, err := s.accounts.Account(ctx, accountID)
	if err != nil {
		return Resolution{}, err
	}
	now := s.now()
	e2e, err := pix.NewEndToEndID(s.ispb, now)
	if err != nil {
		return Resolution{}, err
	}
	res := Resolution{AccountID: acct.ID, Key: key, EndToEndID: e2e, ExpiresAt: now.Add(ResolutionTTL)}

	owner, own, err := s.keys.AccountOf(ctx, key)
	if err != nil {
		return Resolution{}, err
	}
	if own {
		if owner == acct.ID {
			return Resolution{}, fmt.Errorf("%w: the key belongs to the paying account", ErrInvalid)
		}
		payee, err := s.accounts.Account(ctx, owner)
		if err != nil {
			return Resolution{}, err
		}
		res.BookTransfer, res.PayeeAccountID = true, payee.ID
		res.Payee = s.partyOf(payee, key)
		return s.store.SaveResolution(ctx, res)
	}

	entry, err := s.dict.GetEntry(ctx, pix.GetEntryParams{Key: key, PayerID: acct.TaxID, EndToEndID: e2e})
	switch {
	case errors.Is(err, pix.ErrNotFound):
		return Resolution{}, ErrKeyNotFound
	case errors.Is(err, pix.ErrBookTransfer):
		slog.Error("the DICT says a key is the wallet's but the wallet has no record of it", "key", key)
		return Resolution{}, ErrKeyNotRecorded
	case errors.Is(err, pix.ErrBadRequest):
		return Resolution{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	case err != nil:
		return Resolution{}, err
	}
	res.Payee = Payee{
		Name: entry.Owner.Name, TaxID: entry.Owner.TaxIDNumber, Participant: entry.Account.Participant,
		Branch: entry.Account.Branch, AccountNumber: entry.Account.AccountNumber, AccountType: entry.Account.AccountType,
		Key: entry.Key,
	}
	return s.store.SaveResolution(ctx, res)
}

func (s *Service) partyOf(a ledger.Account, key string) Payee {
	return Payee{
		Name: a.HolderName, TaxID: a.TaxID, Participant: s.ispb, Branch: a.Branch, AccountNumber: a.Number,
		AccountType: pix.AccountType(a.Type), Key: key,
	}
}

// PayInput is the request to pay a resolved key.
type PayInput struct {
	ResolutionID string
	AmountCents  int64
	Message      string
}

// Pay pays a resolved key. It reserves the amount and sends the order to the SPI; the payment is Pending until the SPI
// answers, which the worker handles. One resolution makes at most one payment, so a call can be repeated after any
// failure: it returns the payment that exists, or finishes the one that stopped halfway. A repeat with another amount or
// message is refused with ErrResolutionConflict.
//
// When the SPI cannot be reached the payment is still created, Pending and not yet sent, and the worker sends it later;
// the SPI is idempotent by EndToEndId, so sending twice is safe.
func (s *Service) Pay(ctx context.Context, in PayInput) (Payment, error) {
	switch {
	case in.ResolutionID == "":
		return Payment{}, fmt.Errorf("%w: resolution is required", ErrInvalid)
	case in.AmountCents <= 0:
		return Payment{}, fmt.Errorf("%w: amount must be positive", ErrInvalid)
	case utf8.RuneCountInString(in.Message) > MaxMessage:
		return Payment{}, fmt.Errorf("%w: message is longer than %d characters", ErrInvalid, MaxMessage)
	}

	res, found, err := s.store.Resolution(ctx, in.ResolutionID)
	if err != nil {
		return Payment{}, err
	}
	if !found {
		return Payment{}, ErrResolutionNotFound
	}
	if existing, found, err := s.store.ByResolution(ctx, res.ID); err != nil {
		return Payment{}, err
	} else if found {
		return s.repeat(ctx, existing, in)
	}
	if !s.now().Before(res.ExpiresAt) {
		return Payment{}, ErrResolutionNotFound
	}

	acct, err := s.accounts.Account(ctx, res.AccountID)
	if err != nil {
		return Payment{}, err
	}
	if res.BookTransfer {
		return s.payInside(ctx, res, in)
	}
	// The reference is the EndToEndId, so reserving again for the same payment moves nothing.
	rsv, err := s.accounts.Reserve(ctx, acct.ID, in.AmountCents, res.EndToEndID)
	if errors.Is(err, ledger.ErrReferenceConflict) {
		return Payment{}, ErrResolutionConflict // an earlier attempt reserved another amount and stopped halfway
	}
	if err != nil {
		return Payment{}, err
	}

	at := s.now()
	stored, created, err := s.store.Insert(ctx, Payment{
		AccountID: acct.ID, Direction: Outbound, Type: TypePix, Status: Pending, AmountCents: in.AmountCents,
		EndToEndID: res.EndToEndID, ResolutionID: res.ID, ReservationID: rsv.ID, Counterparty: res.Payee,
		Message: in.Message, CreatedAt: at,
	})
	if err != nil {
		// Nothing was recorded, so the money must not stay held. If this fails too, repeating the call finishes it.
		if rerr := s.accounts.Release(context.WithoutCancel(ctx), rsv.ID); rerr != nil {
			slog.Error("release after a payment that was not stored", "end_to_end_id", res.EndToEndID, "err", rerr)
		}
		return Payment{}, err
	}
	if !created {
		return s.repeat(ctx, stored, in) // another call for the same resolution won
	}
	return s.submit(ctx, stored, acct)
}

// repeat answers a call for a resolution that already made a payment.
func (s *Service) repeat(ctx context.Context, p Payment, in PayInput) (Payment, error) {
	if p.AmountCents != in.AmountCents || p.Message != in.Message {
		return Payment{}, ErrResolutionConflict
	}
	if p.Type == TypeBookTransfer {
		if p.Status == Pending {
			return s.completeBookTransfer(ctx, p) // an earlier call stopped halfway
		}
		return p, nil
	}
	if p.Status == Pending && !p.Submitted() {
		acct, err := s.accounts.Account(ctx, p.AccountID)
		if err != nil {
			return Payment{}, err
		}
		return s.submit(ctx, p, acct)
	}
	return p, nil
}

// submit sends the order to the SPI. An answer of "unavailable" is not a failure of the payment: it stays pending and
// unsent, and the worker sends it again. A definite refusal of the message (400) means the SPI never took it, so the
// payment is rejected and the money goes back.
func (s *Service) submit(ctx context.Context, p Payment, acct ledger.Account) (Payment, error) {
	_, body, err := pix.NewPacs008(s.ispb, pix.PaymentOrder{
		EndToEndID:  p.EndToEndID,
		AmountCents: p.AmountCents,
		Payer: pix.Party{
			ISPB: s.ispb, Name: acct.HolderName, TaxID: acct.TaxID, Branch: acct.Branch, Account: acct.Number,
			AccountType: pix.AccountType(acct.Type),
		},
		Receiver: pix.Party{
			ISPB: p.Counterparty.Participant, TaxID: p.Counterparty.TaxID, Branch: p.Counterparty.Branch,
			Account: p.Counterparty.AccountNumber, AccountType: p.Counterparty.AccountType,
		},
		Key:        p.Counterparty.Key,
		Message:    p.Message,
		AcceptedAt: p.CreatedAt,
	}, s.now())
	if err != nil {
		return p, fmt.Errorf("build the payment order: %w", err)
	}

	ids, err := s.bank.Send(ctx, body)
	switch {
	case errors.Is(err, pix.ErrBadRequest):
		slog.Error("the SPI refused the payment order", "end_to_end_id", p.EndToEndID, "err", err)
		return s.reject(ctx, p, pix.Rejection{Code: "SPI400", Cause: pix.CauseWallet, Message: "The payment could not be processed. It was not made."})
	case err != nil:
		slog.Warn("the SPI did not take the payment order; the worker sends it again", "end_to_end_id", p.EndToEndID, "err", err)
		return p, nil
	}

	at := s.now()
	resource := ""
	if len(ids) > 0 {
		resource = ids[0]
	}
	if err := s.store.MarkSubmitted(ctx, p.ID, resource, at); err != nil {
		// The SPI has it. Not recording that is harmless: the worker sends it once more and gets the same outcome.
		slog.Warn("could not record that the SPI took the payment order", "end_to_end_id", p.EndToEndID, "err", err)
		return p, nil
	}
	p.ResourceID, p.SubmittedAt = resource, at
	return p, nil
}

// Get returns a payment.
func (s *Service) Get(ctx context.Context, id string) (Payment, error) {
	if id == "" {
		return Payment{}, fmt.Errorf("%w: payment is required", ErrInvalid)
	}
	p, found, err := s.store.ByID(ctx, id)
	if err != nil {
		return Payment{}, err
	}
	if !found {
		return Payment{}, ErrPaymentNotFound
	}
	return p, nil
}
