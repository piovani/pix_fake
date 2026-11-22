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

var (
	ErrInvalid            = errors.New("payment: invalid request")
	ErrKeyNotFound        = errors.New("payment: key not found")
	ErrResolutionNotFound = errors.New("payment: resolution not found or expired")
	ErrResolutionConflict = errors.New("payment: resolution reused with other data")
	ErrPaymentNotFound    = errors.New("payment: payment not found")
	ErrNotReturnable      = errors.New("payment: not returnable")
	ErrReturnTooLarge     = errors.New("payment: amount above what is left")
	ErrRequestConflict    = errors.New("payment: request id already used with other data")
	ErrKeyNotRecorded     = errors.New("payment: own key not recorded")
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
	ResolutionTTL = 5 * time.Minute
	// as in the pacs.008
	MaxMessage = 140
	// older than this before the worker sends an unsent payment, so it does not race with the call that created it
	resubmitAfter = 10 * time.Second
)

type Payee struct {
	Name          string
	TaxID         string
	Participant   string // ISPB of the institution that holds the account
	Branch        string
	AccountNumber string
	AccountType   pix.AccountType
	Key           string
}

// The EndToEndId is made before the lookup and is the one the payment carries.
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

type Payment struct {
	ID                string
	AccountID         string
	Direction         Direction
	Type              Type
	Status            Status
	AmountCents       int64
	EndToEndID        string
	ResolutionID      string
	ReservationID     string
	OriginalPaymentID string
	RequestID         string
	ReturnedCents     int64
	Counterparty      Payee
	Message           string
	RejectCode        string
	RejectReason      string
	ResourceID        string

	SubmittedAt time.Time
	CreatedAt   time.Time
	FinishedAt  time.Time

	ReportStatus string
	ReportSentAt time.Time
	ReportReason string
}

const (
	ReportSent     = "SENT"
	ReportAccepted = "ACCEPTED"
	ReportRejected = "REJECTED"
)

func (p Payment) Submitted() bool { return !p.SubmittedAt.IsZero() }

type Accounts interface {
	Account(ctx context.Context, id string) (ledger.Account, error)
	Reserve(ctx context.Context, accountID string, cents int64, reference string) (ledger.Reservation, error)
	Settle(ctx context.Context, reservationID string) error
	Release(ctx context.Context, reservationID string) error
	Transfer(ctx context.Context, fromAccountID, toAccountID string, cents int64, reference string) error
	Credit(ctx context.Context, accountID string, cents int64, reference string) error
	AccountByNumber(ctx context.Context, branch, number string) (ledger.Account, error)
	Movements(ctx context.Context, accountID string, beforeID int64, limit int) ([]ledger.Movement, error)
	HeldBefore(ctx context.Context, before time.Time, limit int) ([]ledger.Reservation, error)
}

type Keys interface {
	AccountOf(ctx context.Context, key string) (accountID string, found bool, err error)
}

type Directory interface {
	GetEntry(ctx context.Context, p pix.GetEntryParams) (pix.Entry, error)
}

type Bank interface {
	Send(ctx context.Context, bodies ...[]byte) ([]string, error)
}

type Store interface {
	SaveResolution(ctx context.Context, r Resolution) (Resolution, error)
	Resolution(ctx context.Context, id string) (Resolution, bool, error)

	Insert(ctx context.Context, p Payment) (stored Payment, created bool, err error)
	Discard(ctx context.Context, id string) error
	ByID(ctx context.Context, id string) (Payment, bool, error)
	ByEndToEnd(ctx context.Context, endToEndID string, direction Direction) (Payment, bool, error)
	ByResolution(ctx context.Context, resolutionID string) (Payment, bool, error)
	ByResource(ctx context.Context, resourceID string) (Payment, bool, error)
	ByRequestID(ctx context.Context, requestID string) (Payment, bool, error)
	ByReservation(ctx context.Context, reservationID string) (Payment, bool, error)
	ByReferences(ctx context.Context, accountID string, refs []string) ([]Payment, error)
	InsertReturn(ctx context.Context, p Payment) (stored Payment, created bool, err error)
	MarkSubmitted(ctx context.Context, id, resourceID string, at time.Time) error
	Close(ctx context.Context, id string, status Status, rejectCode, rejectReason string, at time.Time) (bool, error)
	Unsent(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	List(ctx context.Context, q Query) ([]Payment, error)

	PendingBookTransfers(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	UnreportedBookTransfers(ctx context.Context, before time.Time, limit int) ([]Payment, error)
	SetReport(ctx context.Context, id, status, reason string, at time.Time) error
	RejectedReports(ctx context.Context, limit int) ([]Payment, error)
	ResetReport(ctx context.Context, id string) (bool, error)
}

type Query struct {
	AccountID string
	Statuses  []Status
	After     *Cursor
	Limit     int
}

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

// A key of another customer of the wallet never goes to the DICT.
func (s *Service) ResolveKey(ctx context.Context, accountID, key string) (Resolution, error) {
	if accountID == "" || key == "" {
		return Resolution{}, fmt.Errorf("%w: account and key required", ErrInvalid)
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
			return Resolution{}, fmt.Errorf("%w: key belongs to the payer", ErrInvalid)
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
		slog.Error("own key missing locally", "key", key)
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

type PayInput struct {
	ResolutionID string
	AmountCents  int64
	Message      string
}

// One resolution makes at most one payment; repeating the call finishes it, and another amount or message is a conflict.
func (s *Service) Pay(ctx context.Context, in PayInput) (Payment, error) {
	switch {
	case in.ResolutionID == "":
		return Payment{}, fmt.Errorf("%w: resolution required", ErrInvalid)
	case in.AmountCents <= 0:
		return Payment{}, fmt.Errorf("%w: amount must be positive", ErrInvalid)
	case utf8.RuneCountInString(in.Message) > MaxMessage:
		return Payment{}, fmt.Errorf("%w: message over %d chars", ErrInvalid, MaxMessage)
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
	if rsv.Status != ledger.Held {
		return Payment{}, ErrResolutionNotFound // released by the sweep of orphans: the money is not held
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
			slog.Error("release after failed insert", "end_to_end_id", res.EndToEndID, "err", rerr)
		}
		return Payment{}, err
	}
	if !created {
		return s.repeat(ctx, stored, in) // another call for the same resolution won
	}
	return s.submit(ctx, stored)
}

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
		return s.submit(ctx, p)
	}
	return p, nil
}

// An "unavailable" SPI leaves the payment pending for the worker; a 400 means the SPI never took it, so it is rejected.
func (s *Service) submit(ctx context.Context, p Payment) (Payment, error) {
	body, err := s.orderOf(ctx, p)
	if err != nil {
		return p, err
	}

	ids, err := s.bank.Send(ctx, body)
	switch {
	case errors.Is(err, pix.ErrBadRequest):
		slog.Error("order refused by spi", "payment", p.ID, "type", p.Type, "err", err)
		return s.reject(ctx, p, pix.Rejection{Code: "SPI400", Cause: pix.CauseWallet, Message: "The payment could not be processed. It was not made."})
	case err != nil:
		slog.Warn("spi unavailable, worker will resend", "payment", p.ID, "type", p.Type, "err", err)
		return p, nil
	}

	at := s.now()
	resource := ""
	if len(ids) > 0 {
		resource = ids[0]
	}
	if err := s.store.MarkSubmitted(ctx, p.ID, resource, at); err != nil {
		// the SPI has it; the worker sends it once more and gets the same outcome
		slog.Warn("mark submitted failed", "payment", p.ID, "err", err)
		return p, nil
	}
	p.ResourceID, p.SubmittedAt = resource, at
	return p, nil
}

func (s *Service) orderOf(ctx context.Context, p Payment) ([]byte, error) {
	if p.Type == TypeReturn {
		return s.returnOrderOf(ctx, p)
	}
	acct, err := s.accounts.Account(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
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
	return body, err
}

func (s *Service) Get(ctx context.Context, id string) (Payment, error) {
	if id == "" {
		return Payment{}, fmt.Errorf("%w: payment required", ErrInvalid)
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
