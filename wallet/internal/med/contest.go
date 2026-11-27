package med

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

// The wallet as the payer's participant: a customer contests a payment they made. The wallet reports it in the DICT
// (REFUND_REQUEST); when the participant that received it agrees, the wallet asks for the refund (FRAUD), and the
// money comes back as a return (pacs.004 FR01) that the payment module credits like any other.

var (
	ErrContestNotFound  = errors.New("med: contest not found")
	ErrNotContestable   = errors.New("med: payment not contestable")
	ErrAlreadyContested = errors.New("med: payment already being contested")
	ErrRequestConflict  = errors.New("med: request id already used with other data")
	ErrNotCancellable   = errors.New("med: contest can no longer be cancelled")
)

const (
	ContestOpening         = "OPENING"
	ContestReported        = "REPORTED"
	ContestAgreed          = "AGREED"
	ContestRefundRequested = "REFUND_REQUESTED"
	ContestRefunded        = "REFUNDED"
	ContestRejected        = "REJECTED"
	ContestCancelled       = "CANCELLED"
)

// contestWindow is how long after a payment its payer's participant can report it (the MED takes reports for 80 days).
const contestWindow = 80 * 24 * time.Hour

// The situations of an infraction report, as the DICT names them.
var Situations = []string{"SCAM", "ACCOUNT_TAKEOVER", "COERCION", "FRAUDULENT_ACCESS", "OTHER"}

const maxDetails = 2000

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Contest struct {
	ID            string
	AccountID     string
	PaymentID     string
	TransactionID string
	RequestID     string
	Situation     string
	Details       string
	AmountCents   int64
	Status        string
	ReportID      string
	RefundID      string
	RefundedCents int64
	ReturnID      string
	Rejection     string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ContestDirectory is what the payer's side needs of the DICT, besides Directory.
type ContestDirectory interface {
	CreateInfractionReport(ctx context.Context, r pix.NewReport) (pix.InfractionReport, error)
	CancelInfractionReport(ctx context.Context, id string) (pix.InfractionReport, error)
	CreateRefund(ctx context.Context, transactionID, reason string, cents int64, details string) (pix.Refund, error)
}

type ContestStore interface {
	// InsertContest stores a new contest. One with the same RequestID is returned with created == false; another one in
	// progress for the payment is ErrAlreadyContested.
	InsertContest(ctx context.Context, c Contest) (stored Contest, created bool, err error)
	Contest(ctx context.Context, id string) (Contest, bool, error)
	ContestByReport(ctx context.Context, reportID string) (Contest, bool, error)
	ContestByRefund(ctx context.Context, refundID string) (Contest, bool, error)
	ContestsOf(ctx context.Context, accountID string) ([]Contest, error)
	// ContestsToSubmit are the ones whose report (OPENING) or refund (AGREED) is not in the DICT yet.
	ContestsToSubmit(ctx context.Context, limit int) ([]Contest, error)
	SaveContest(ctx context.Context, c Contest) error
}

type ContestInput struct {
	PaymentID string
	Situation string // one of Situations
	Details   string // required for OTHER
	RequestID string // UUID v4
}

// Contest opens a contest of a payment the account made through the SPI, and reports it in the DICT. When the DICT
// cannot be reached the contest stays OPENING and the worker reports it.
func (s *Service) Contest(ctx context.Context, in ContestInput) (Contest, error) {
	in.Details = strings.TrimSpace(in.Details)
	switch {
	case in.PaymentID == "":
		return Contest{}, fmt.Errorf("%w: payment required", ErrInvalid)
	case !slices.Contains(Situations, in.Situation):
		return Contest{}, fmt.Errorf("%w: situation must be one of %v", ErrInvalid, Situations)
	case in.Situation == "OTHER" && in.Details == "":
		return Contest{}, fmt.Errorf("%w: details are required for OTHER", ErrInvalid)
	case utf8.RuneCountInString(in.Details) > maxDetails:
		return Contest{}, fmt.Errorf("%w: details longer than %d characters", ErrInvalid, maxDetails)
	case !uuidV4.MatchString(in.RequestID):
		return Contest{}, fmt.Errorf("%w: request id must be uuid v4", ErrInvalid)
	}
	pay, err := s.payments.Get(ctx, in.PaymentID)
	if err != nil {
		return Contest{}, err
	}
	switch {
	case pay.Direction != payment.Outbound || pay.Type != payment.TypePix:
		return Contest{}, fmt.Errorf("%w: only payments sent to another bank", ErrNotContestable)
	case pay.Status != payment.Settled:
		return Contest{}, fmt.Errorf("%w: not settled", ErrNotContestable)
	case s.now().Sub(pay.FinishedAt) > contestWindow:
		return Contest{}, fmt.Errorf("%w: older than 80 days", ErrNotContestable)
	case pay.AmountCents-pay.ReturnedCents <= 0:
		return Contest{}, fmt.Errorf("%w: already returned", ErrNotContestable)
	}
	now := s.now()
	c, created, err := s.store.InsertContest(ctx, Contest{
		AccountID: pay.AccountID, PaymentID: pay.ID, TransactionID: pay.EndToEndID, RequestID: in.RequestID,
		Situation: in.Situation, Details: in.Details, AmountCents: pay.AmountCents - pay.ReturnedCents, Status: ContestOpening,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return Contest{}, err
	}
	if !created {
		if c.PaymentID != in.PaymentID || c.Situation != in.Situation || c.Details != in.Details {
			return Contest{}, ErrRequestConflict
		}
		return c, nil
	}
	contests.Add(ctx, 1)
	if c, err = s.report(ctx, c); err != nil {
		slog.WarnContext(ctx, "contest not reported yet, the worker will", "contest", c.ID, "err", err)
	}
	return c, nil
}

func (s *Service) GetContest(ctx context.Context, id string) (Contest, error) {
	c, found, err := s.store.Contest(ctx, id)
	if err != nil {
		return Contest{}, err
	}
	if !found {
		return Contest{}, ErrContestNotFound
	}
	return c, nil
}

func (s *Service) Contests(ctx context.Context, accountID string) ([]Contest, error) {
	if accountID == "" {
		return nil, fmt.Errorf("%w: account required", ErrInvalid)
	}
	return s.store.ContestsOf(ctx, accountID)
}

// CancelContest withdraws a contest whose report the other participant has not analysed yet.
func (s *Service) CancelContest(ctx context.Context, id string) (Contest, error) {
	c, err := s.GetContest(ctx, id)
	if err != nil {
		return Contest{}, err
	}
	switch c.Status {
	case ContestCancelled:
		return c, nil
	case ContestOpening, ContestReported:
	default:
		return Contest{}, fmt.Errorf("%w: it is %s", ErrNotCancellable, c.Status)
	}
	if c.ReportID != "" {
		r, err := s.dict.CancelInfractionReport(ctx, c.ReportID)
		if err != nil {
			var api *pix.APIError
			if errors.As(err, &api) && api.Status < 500 && api.Status != 429 {
				return Contest{}, fmt.Errorf("%w: %s", ErrNotCancellable, api.Detail)
			}
			return Contest{}, err
		}
		if r.Status != ReportCancelled {
			return Contest{}, fmt.Errorf("%w: the report is %s", ErrNotCancellable, r.Status)
		}
	}
	c.Status = ContestCancelled
	return c, s.saveContest(ctx, c)
}

// report creates the infraction report of a contest that is OPENING. A report already in progress for the
// transaction (a call whose answer was lost) is found and taken.
func (s *Service) report(ctx context.Context, c Contest) (Contest, error) {
	r, err := s.dict.CreateInfractionReport(ctx, pix.NewReport{TransactionID: c.TransactionID, SituationType: c.Situation, Details: c.Details})
	if errors.Is(err, pix.ErrConflict) {
		r, err = s.reportInProgress(ctx, c.TransactionID)
	}
	if err != nil {
		var api *pix.APIError
		if errors.As(err, &api) && api.Status == 400 {
			c.Status, c.Rejection = ContestRejected, "The DICT refused the report: "+api.Detail
			return c, s.saveContest(ctx, c)
		}
		return c, err
	}
	// cancelled while the report was being made: withdraw it
	if current, found, err := s.store.Contest(ctx, c.ID); err == nil && found && current.Status == ContestCancelled {
		_, err := s.dict.CancelInfractionReport(ctx, r.ID)
		return current, err
	}
	c.Status, c.ReportID = ContestReported, r.ID
	return c, s.saveContest(ctx, c)
}

func (s *Service) reportInProgress(ctx context.Context, transactionID string) (pix.InfractionReport, error) {
	reports, _, err := s.dict.ListInfractionReports(ctx, pix.ListReportsParams{AsReporter: true, Statuses: []string{ReportOpen, ReportAcknowledged}, Limit: listLimit})
	if err != nil {
		return pix.InfractionReport{}, err
	}
	for _, r := range reports {
		if r.TransactionID == transactionID && r.Reason == "REFUND_REQUEST" {
			return r, nil
		}
	}
	return pix.InfractionReport{}, fmt.Errorf("the DICT has a report in progress for %s that the wallet cannot find", transactionID)
}

// askRefund creates the refund of a contest whose report was AGREED. One already open (a call whose answer was lost) is
// found and taken.
func (s *Service) askRefund(ctx context.Context, c Contest) (Contest, error) {
	f, err := s.dict.CreateRefund(ctx, c.TransactionID, "FRAUD", c.AmountCents, c.Details)
	if errors.Is(err, pix.ErrConflict) {
		f, err = s.refundOpen(ctx, c.TransactionID)
	}
	if err != nil {
		return c, err
	}
	c.Status, c.RefundID = ContestRefundRequested, f.ID
	return c, s.saveContest(ctx, c)
}

func (s *Service) refundOpen(ctx context.Context, transactionID string) (pix.Refund, error) {
	cursor := ""
	for {
		page, err := s.dict.ListRefunds(ctx, pix.ListRefundsParams{Statuses: []string{"OPEN"}, Cursor: cursor, Limit: listLimit})
		if err != nil {
			return pix.Refund{}, err
		}
		for _, f := range page.Refunds {
			if f.TransactionID == transactionID {
				return f, nil
			}
		}
		if !page.HasMore || page.NextCursor == "" {
			return pix.Refund{}, fmt.Errorf("the DICT has an open refund for %s that the wallet cannot find", transactionID)
		}
		cursor = page.NextCursor
	}
}

// SyncContests moves the contests on: it reports the ones still OPENING, follows the analysis of the reports, asks for
// the refund of the agreed ones and records how each refund ended. It returns how many contests changed.
func (s *Service) SyncContests(ctx context.Context) (int, error) {
	n := 0
	pending, err := s.store.ContestsToSubmit(ctx, 50)
	if err != nil {
		return 0, err
	}
	for _, c := range pending {
		var err error
		if c.Status == ContestOpening {
			_, err = s.report(ctx, c)
		} else {
			_, err = s.askRefund(ctx, c)
		}
		if err != nil {
			return n, fmt.Errorf("contest %s: %w", c.ID, err)
		}
		n++
	}
	m, err := s.followReports(ctx)
	n += m
	if err != nil {
		return n, err
	}
	m, err = s.followRefunds(ctx)
	return n + m, err
}

func (s *Service) followReports(ctx context.Context) (int, error) {
	const name = "reported-infraction-reports"
	since, err := s.store.Watermark(ctx, name)
	if err != nil {
		return 0, err
	}
	n := 0
	for {
		reports, more, err := s.dict.ListInfractionReports(ctx, pix.ListReportsParams{AsReporter: true, ModifiedAfter: since, Limit: listLimit})
		if err != nil {
			return n, err
		}
		latest := since
		for _, r := range reports {
			latest = maxTime(latest, r.Modified)
			c, found, err := s.store.ContestByReport(ctx, r.ID)
			if err != nil {
				return n, err
			}
			if !found || c.Status != ContestReported {
				continue
			}
			switch {
			case r.Status == ReportClosed && r.AnalysisResult == Agreed:
				c.Status = ContestAgreed
				if err := s.saveContest(ctx, c); err != nil {
					return n, err
				}
				if _, err := s.askRefund(ctx, c); err != nil {
					slog.WarnContext(ctx, "refund not asked yet, trying again later", "contest", c.ID, "err", err)
				}
			case r.Status == ReportClosed:
				c.Status, c.Rejection = ContestRejected, "The bank that received the payment did not find a fraud."
				if err := s.saveContest(ctx, c); err != nil {
					return n, err
				}
			case r.Status == ReportCancelled:
				c.Status = ContestCancelled
				if err := s.saveContest(ctx, c); err != nil {
					return n, err
				}
			default:
				continue
			}
			n++
		}
		if err := s.store.SetWatermark(ctx, name, latest); err != nil {
			return n, err
		}
		if !more || !latest.After(since) {
			return n, nil
		}
		since = latest
	}
}

var rejections = map[string]string{
	"NO_BALANCE":      "The account that received the payment has no money left to give back.",
	"ACCOUNT_CLOSURE": "The account that received the payment was closed.",
	"INVALID_REQUEST": "The bank that received the payment refused the request.",
	"OTHER":           "The bank that received the payment could not give the money back.",
}

func (s *Service) followRefunds(ctx context.Context) (int, error) {
	const name = "requested-refunds"
	since, err := s.store.Watermark(ctx, name)
	if err != nil {
		return 0, err
	}
	n, cursor, latest := 0, "", since
	for {
		page, err := s.dict.ListRefunds(ctx, pix.ListRefundsParams{ModifiedAfter: since, Cursor: cursor, Limit: listLimit})
		if err != nil {
			return n, err
		}
		for _, f := range page.Refunds {
			latest = maxTime(latest, f.Modified)
			if f.Status == "OPEN" {
				continue
			}
			c, found, err := s.store.ContestByRefund(ctx, f.ID)
			if err != nil {
				return n, err
			}
			if !found || c.Status != ContestRefundRequested {
				continue
			}
			switch {
			case f.Status == "CLOSED" && f.AnalysisResult != "REJECTED":
				c.Status, c.ReturnID, c.RefundedCents = ContestRefunded, f.RefundTransactionID, f.AmountCents
				if ret, found, err := s.payments.Received(ctx, f.RefundTransactionID); err == nil && found {
					c.RefundedCents = ret.AmountCents
				}
			case f.Status == "CLOSED":
				c.Status, c.Rejection = ContestRejected, rejections[f.RejectionReason]
				if c.Rejection == "" {
					c.Rejection = rejections["OTHER"]
				}
			default: // cancelled
				c.Status = ContestCancelled
			}
			if err := s.saveContest(ctx, c); err != nil {
				return n, err
			}
			n++
		}
		if !page.HasMore || page.NextCursor == "" {
			return n, s.store.SetWatermark(ctx, name, latest)
		}
		cursor = page.NextCursor
	}
}

func (s *Service) saveContest(ctx context.Context, c Contest) error {
	c.UpdatedAt = s.now()
	return s.store.SaveContest(ctx, c)
}
