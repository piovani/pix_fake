package med

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

var (
	ErrReportNotFound = errors.New("med: infraction report not found")
	ErrNotAnalysable  = errors.New("med: infraction report is not waiting for an analysis")
	ErrInvalid        = errors.New("med: invalid request")
)

// The statuses of a report are the DICT's.
const (
	ReportOpen         = "OPEN"
	ReportAcknowledged = "ACKNOWLEDGED"
	ReportClosed       = "CLOSED"
	ReportCancelled    = "CANCELLED"

	Agreed    = "AGREED"
	Disagreed = "DISAGREED"

	BlockHeld     = "HELD"
	BlockReleased = "RELEASED"
	BlockReturned = "RETURNED"

	RefundReturning = "RETURNING"
	RefundClosed    = "CLOSED"
)

// The fraud types an AGREED analysis can give.
var FraudTypes = []string{"APPLICATION_FRAUD", "MULE_ACCOUNT", "SCAMMER_ACCOUNT", "OTHER"}

type Report struct {
	ID            string
	TransactionID string
	PaymentID     string // empty when the wallet has not that payment
	AccountID     string
	Status        string
	Situation     string
	Details       string
	AmountCents   int64 // what the DICT asks to block
	BlockID       string
	BlockedCents  int64
	BlockStatus   string
	Result        string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type Refund struct {
	ID              string
	TransactionID   string
	PaymentID       string
	Reason          string // of the DICT: FRAUD, OPERATIONAL_FLAW
	AmountCents     int64
	Status          string
	RequestID       string
	ReturnCents     int64
	ReturnPaymentID string
	Result          string
	RejectionReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Directory interface {
	ListInfractionReports(ctx context.Context, p pix.ListReportsParams) ([]pix.InfractionReport, bool, error)
	AcknowledgeInfractionReport(ctx context.Context, id string) (pix.InfractionReport, error)
	CloseInfractionReport(ctx context.Context, id, result, fraudType, details string) (pix.InfractionReport, error)
	ListRefunds(ctx context.Context, p pix.ListRefundsParams) (pix.RefundsPage, error)
	GetRefund(ctx context.Context, id string) (pix.Refund, error)
	CloseRefund(ctx context.Context, p pix.CloseRefundParams) (pix.Refund, error)
	ContestDirectory
}

type Accounts interface {
	Block(ctx context.Context, accountID string, upTo int64, reference string) (ledger.Reservation, error)
	Release(ctx context.Context, reservationID string) error
	Balance(ctx context.Context, accountID string) (ledger.Balance, error)
}

type Payments interface {
	Received(ctx context.Context, endToEndID string) (payment.Payment, bool, error)
	Get(ctx context.Context, id string) (payment.Payment, error)
	Return(ctx context.Context, in payment.ReturnInput) (payment.Payment, error)
}

type Store interface {
	Report(ctx context.Context, id string) (Report, bool, error)
	SaveReport(ctx context.Context, r Report) error
	// BlockedFor returns the AGREED report of the transaction whose block is still held.
	BlockedFor(ctx context.Context, transactionID string) (Report, bool, error)
	AwaitingAnalysis(ctx context.Context) ([]Report, error)

	Refund(ctx context.Context, id string) (Refund, bool, error)
	// InsertRefund stores a new refund; one already stored is returned with created == false.
	InsertRefund(ctx context.Context, f Refund) (stored Refund, created bool, err error)
	SaveRefund(ctx context.Context, f Refund) error
	Returning(ctx context.Context, limit int) ([]Refund, error)

	Watermark(ctx context.Context, name string) (time.Time, error)
	SetWatermark(ctx context.Context, name string, at time.Time) error

	ContestStore
}

type Service struct {
	dict     Directory
	accounts Accounts
	payments Payments
	store    Store
	now      func() time.Time
}

func New(dict Directory, accounts Accounts, payments Payments, store Store) *Service {
	return &Service{dict: dict, accounts: accounts, payments: payments, store: store, now: time.Now}
}

const listLimit = 200

// SyncReports reads the changes of the reports against the wallet since the last call and acts on them: a new one is
// acknowledged and its money blocked, a cancelled one gives the block back. It returns how many it acted on.
func (s *Service) SyncReports(ctx context.Context) (int, error) {
	const name = "infraction-reports"
	since, err := s.store.Watermark(ctx, name)
	if err != nil {
		return 0, err
	}
	n := 0
	for {
		reports, more, err := s.dict.ListInfractionReports(ctx, pix.ListReportsParams{AsCounterparty: true, ModifiedAfter: since, Limit: listLimit})
		if err != nil {
			return n, err
		}
		latest := since
		for _, r := range reports {
			acted, err := s.onReport(ctx, r)
			if err != nil {
				return n, fmt.Errorf("report %s: %w", r.ID, err)
			}
			if acted {
				n++
			}
			latest = maxTime(latest, r.Modified)
		}
		// ModifiedAfter is inclusive: the last ones come again, and acting on a report twice changes nothing.
		if err := s.store.SetWatermark(ctx, name, latest); err != nil {
			return n, err
		}
		if !more || !latest.After(since) {
			return n, nil
		}
		since = latest
	}
}

func (s *Service) onReport(ctx context.Context, r pix.InfractionReport) (bool, error) {
	if r.Reason != "REFUND_REQUEST" {
		return false, nil // REFUND_CANCELLED is the wallet as the payer's participant: not here yet
	}
	local, found, err := s.store.Report(ctx, r.ID)
	if err != nil {
		return false, err
	}
	switch r.Status {
	case ReportOpen, ReportAcknowledged:
		if !found {
			if local, err = s.block(ctx, r); err != nil {
				return false, err
			}
		}
		if r.Status == ReportOpen {
			if _, err := s.dict.AcknowledgeInfractionReport(ctx, r.ID); err != nil {
				return false, err
			}
		}
		if found && local.Status != ReportOpen {
			return false, nil
		}
		local.Status = ReportAcknowledged
		return true, s.saveReport(ctx, local)
	case ReportCancelled:
		if !found || local.Status == ReportCancelled {
			return false, nil
		}
		if err := s.release(ctx, &local); err != nil {
			return false, err
		}
		local.Status = ReportCancelled
		slog.InfoContext(ctx, "infraction report cancelled, block released", "report", r.ID, "transaction", r.TransactionID)
		return true, s.saveReport(ctx, local)
	case ReportClosed:
		// closed by the analysis of this wallet; a report the wallet never saw open has nothing to undo
		if !found || local.Result != "" {
			return false, nil
		}
		local.Status, local.Result = ReportClosed, r.AnalysisResult
		if local.Result == Disagreed {
			if err := s.release(ctx, &local); err != nil {
				return false, err
			}
		}
		return true, s.saveReport(ctx, local)
	}
	return false, nil
}

// block records a new report and blocks its money: what the DICT asks, or what the account has when it is less.
func (s *Service) block(ctx context.Context, r pix.InfractionReport) (Report, error) {
	local := Report{
		ID: r.ID, TransactionID: r.TransactionID, Status: ReportOpen, Situation: r.SituationType, Details: r.Details,
		AmountCents: r.AmountCents, CreatedAt: s.now(),
	}
	pay, found, err := s.payments.Received(ctx, r.TransactionID)
	if err != nil {
		return Report{}, err
	}
	if !found || pay.Status != payment.Settled {
		slog.WarnContext(ctx, "infraction report on a payment the wallet did not receive", "report", r.ID, "transaction", r.TransactionID)
		return local, s.saveReport(ctx, local)
	}
	local.PaymentID, local.AccountID = pay.ID, pay.AccountID
	if local.AmountCents == 0 {
		local.AmountCents = pay.AmountCents
	}
	b, err := s.accounts.Block(ctx, pay.AccountID, local.AmountCents, "med-block:"+r.ID)
	switch {
	case errors.Is(err, ledger.ErrInsufficientFunds):
		slog.WarnContext(ctx, "nothing to block for an infraction report", "report", r.ID, "account", pay.AccountID)
	case err != nil:
		return Report{}, err
	default:
		local.BlockID, local.BlockedCents, local.BlockStatus = b.ID, b.AmountCents, BlockHeld
		blocked.Add(ctx, b.AmountCents)
		slog.InfoContext(ctx, "money blocked for an infraction report", "report", r.ID, "account", pay.AccountID, "cents", b.AmountCents)
	}
	return local, s.saveReport(ctx, local)
}

func (s *Service) release(ctx context.Context, r *Report) error {
	if r.BlockStatus != BlockHeld {
		return nil
	}
	if err := s.accounts.Release(ctx, r.BlockID); err != nil {
		return err
	}
	r.BlockStatus = BlockReleased
	return nil
}

func (s *Service) saveReport(ctx context.Context, r Report) error {
	r.UpdatedAt = s.now()
	return s.store.SaveReport(ctx, r)
}

// AwaitingAnalysis lists the reports the wallet acknowledged and has not analysed yet.
func (s *Service) AwaitingAnalysis(ctx context.Context) ([]Report, error) {
	return s.store.AwaitingAnalysis(ctx)
}

type Analysis struct {
	ReportID  string
	Agree     bool
	FraudType string // one of FraudTypes, for an agreement
	Details   string
}

// Analyse closes a report with the wallet's analysis. Agreeing keeps the block for the refund that follows;
// disagreeing gives it back.
func (s *Service) Analyse(ctx context.Context, a Analysis) (Report, error) {
	result := Disagreed
	if a.Agree {
		result = Agreed
		if !slices.Contains(FraudTypes, a.FraudType) {
			return Report{}, fmt.Errorf("%w: fraud type must be one of %v", ErrInvalid, FraudTypes)
		}
	} else if a.FraudType != "" {
		return Report{}, fmt.Errorf("%w: a disagreement has no fraud type", ErrInvalid)
	}
	local, found, err := s.store.Report(ctx, a.ReportID)
	switch {
	case err != nil:
		return Report{}, err
	case !found:
		return Report{}, ErrReportNotFound
	case local.Status != ReportAcknowledged || local.Result != "":
		return Report{}, fmt.Errorf("%w: it is %s", ErrNotAnalysable, local.Status)
	}
	if _, err := s.dict.CloseInfractionReport(ctx, a.ReportID, result, a.FraudType, a.Details); err != nil {
		return Report{}, err
	}
	local.Status, local.Result = ReportClosed, result
	if result == Disagreed {
		if err := s.release(ctx, &local); err != nil {
			return Report{}, err
		}
	}
	return local, s.saveReport(ctx, local)
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
