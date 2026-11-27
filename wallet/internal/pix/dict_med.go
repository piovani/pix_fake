package pix

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// MED (Mecanismo Especial de Devolução): the infraction reports and refunds of the DICT. Every call carries
// PI-RequestingParticipant, which the DICT asks of these operations.

type InfractionReport struct {
	ID              string
	TransactionID   string // EndToEndId of the payment reported
	Reason          string // REFUND_REQUEST or REFUND_CANCELLED
	SituationType   string
	Details         string
	Status          string // OPEN, ACKNOWLEDGED, CLOSED, CANCELLED
	Reporter        string // ISPB
	Counterparty    string // ISPB
	AnalysisResult  string // AGREED or DISAGREED, once closed
	FundsRecoveryID string
	AmountCents     int64 // InfractionAmount
	Modified        time.Time
}

type ListReportsParams struct {
	AsCounterparty bool // only the reports against the wallet
	AsReporter     bool // only the reports the wallet made
	Statuses       []string
	ModifiedAfter  time.Time // zero for no bound; inclusive
	Limit          int       // 1..200, 0 for the DICT's default
}

type Refund struct {
	ID              string
	TransactionID   string
	Reason          string // FRAUD, OPERATIONAL_FLAW, REFUND_CANCELLED
	AmountCents     int64
	Details         string
	Status          string // OPEN, CLOSED, CANCELLED
	Requesting      string // ISPB
	Contested       string // ISPB
	ReportID        string
	FundsRecoveryID string
	// Once closed: TOTALLY_ACCEPTED, PARTIALLY_ACCEPTED or REJECTED, with the rejection reason or the RtrId of the return.
	AnalysisResult      string
	RejectionReason     string
	RefundTransactionID string
	Modified            time.Time
}

type ListRefundsParams struct {
	AsContested   bool // the refunds asked of the wallet; false lists the ones it asked
	Statuses      []string
	ModifiedAfter time.Time
	Cursor        string
	Limit         int
}

type RefundsPage struct {
	Refunds    []Refund
	NextCursor string
	HasMore    bool
}

type CloseRefundParams struct {
	ID                  string
	Result              string // TOTALLY_ACCEPTED, PARTIALLY_ACCEPTED, REJECTED
	RejectionReason     string // for REJECTED: NO_BALANCE, ACCOUNT_CLOSURE, CANNOT_REFUND, OTHER...
	RefundTransactionID string // for an accepted one: the RtrId of the return made in the SPI
	Details             string
}

func (c *DictClient) medHeader() http.Header {
	h := http.Header{}
	h.Set("PI-RequestingParticipant", c.ispb)
	return h
}

func (c *DictClient) ListInfractionReports(ctx context.Context, p ListReportsParams) ([]InfractionReport, bool, error) {
	q := url.Values{"Participant": {c.ispb}, "IncludeDetails": {"true"}}
	if p.AsCounterparty {
		q.Set("IsCounterparty", "true")
	}
	if p.AsReporter {
		q.Set("IsReporter", "true")
	}
	if len(p.Statuses) > 0 {
		q.Set("Status", strings.Join(p.Statuses, ","))
	}
	if !p.ModifiedAfter.IsZero() {
		q.Set("ModifiedAfter", p.ModifiedAfter.UTC().Format(timeLayout))
	}
	if p.Limit > 0 {
		q.Set("Limit", strconv.Itoa(p.Limit))
	}
	var out listReportsXML
	if err := c.do(ctx, http.MethodGet, "/infraction-reports?"+q.Encode(), c.medHeader(), nil, http.StatusOK, &out); err != nil {
		return nil, false, err
	}
	reports := make([]InfractionReport, 0, len(out.Reports))
	for _, r := range out.Reports {
		rep, err := r.report()
		if err != nil {
			return nil, false, err
		}
		reports = append(reports, rep)
	}
	return reports, out.HasMore, nil
}

type NewReport struct {
	TransactionID string // EndToEndId of a payment the wallet made
	SituationType string // SCAM, ACCOUNT_TAKEOVER, COERCION, FRAUDULENT_ACCESS, OTHER
	Details       string
}

// CreateInfractionReport reports a payment the wallet made as a fraud (REFUND_REQUEST), so that the participant that
// received it analyses it. One report per transaction can be in progress: another is ErrConflict.
func (c *DictClient) CreateInfractionReport(ctx context.Context, r NewReport) (InfractionReport, error) {
	body := createReportXML{Participant: c.ispb}
	body.Report.TransactionID, body.Report.Reason = r.TransactionID, "REFUND_REQUEST"
	body.Report.SituationType, body.Report.ReportDetails = r.SituationType, r.Details
	var out reportResponseXML
	if err := c.do(ctx, http.MethodPost, "/infraction-reports", c.medHeader(), body, http.StatusCreated, &out); err != nil {
		return InfractionReport{}, err
	}
	return out.Report.report()
}

// CancelInfractionReport withdraws a report the wallet made.
func (c *DictClient) CancelInfractionReport(ctx context.Context, id string) (InfractionReport, error) {
	body := reportActionXML{XMLName: xmlName("CancelInfractionReportRequest"), ID: id, Participant: c.ispb}
	var out reportResponseXML
	if err := c.do(ctx, http.MethodPost, "/infraction-reports/"+url.PathEscape(id)+"/cancel", c.medHeader(), body, http.StatusOK, &out); err != nil {
		return InfractionReport{}, err
	}
	return out.Report.report()
}

// CreateRefund asks the participant that received a payment of the wallet for the money back. FRAUD needs a report of
// the wallet on the transaction closed AGREED. One refund per transaction can be open: another is ErrConflict.
func (c *DictClient) CreateRefund(ctx context.Context, transactionID, reason string, cents int64, details string) (Refund, error) {
	body := createRefundXML{Participant: c.ispb}
	body.Refund.TransactionID, body.Refund.RefundReason = transactionID, reason
	body.Refund.RefundAmount, body.Refund.RefundDetails = formatAmount(cents), details
	var out refundResponseXML
	if err := c.do(ctx, http.MethodPost, "/refunds", c.medHeader(), body, http.StatusCreated, &out); err != nil {
		return Refund{}, err
	}
	return out.Refund.refund()
}

// AcknowledgeInfractionReport tells the DICT the wallet received the report. Repeating it is harmless.
func (c *DictClient) AcknowledgeInfractionReport(ctx context.Context, id string) (InfractionReport, error) {
	body := reportActionXML{XMLName: xmlName("AcknowledgeInfractionReportRequest"), ID: id, Participant: c.ispb}
	var out reportResponseXML
	if err := c.do(ctx, http.MethodPost, "/infraction-reports/"+url.PathEscape(id)+"/acknowledge", c.medHeader(), body, http.StatusOK, &out); err != nil {
		return InfractionReport{}, err
	}
	return out.Report.report()
}

// CloseInfractionReport gives the wallet's analysis: AGREED, with the fraud type, or DISAGREED.
func (c *DictClient) CloseInfractionReport(ctx context.Context, id, result, fraudType, details string) (InfractionReport, error) {
	body := closeReportXML{ID: id, Participant: c.ispb, FraudType: fraudType, AnalysisResult: result, AnalysisDetails: details}
	var out reportResponseXML
	if err := c.do(ctx, http.MethodPost, "/infraction-reports/"+url.PathEscape(id)+"/close", c.medHeader(), body, http.StatusOK, &out); err != nil {
		return InfractionReport{}, err
	}
	return out.Report.report()
}

func (c *DictClient) ListRefunds(ctx context.Context, p ListRefundsParams) (RefundsPage, error) {
	role := "REQUESTING"
	if p.AsContested {
		role = "CONTESTED"
	}
	q := url.Values{"Participant": {c.ispb}, "ParticipantRole": {role}, "IncludeDetails": {"true"}}
	if len(p.Statuses) > 0 {
		q.Set("Status", strings.Join(p.Statuses, ","))
	}
	if !p.ModifiedAfter.IsZero() {
		q.Set("ModifiedAfter", p.ModifiedAfter.UTC().Format(timeLayout))
	}
	if p.Cursor != "" {
		q.Set("Cursor", p.Cursor)
	}
	if p.Limit > 0 {
		q.Set("Limit", strconv.Itoa(p.Limit))
	}
	var out listRefundsXML
	if err := c.do(ctx, http.MethodGet, "/refunds?"+q.Encode(), c.medHeader(), nil, http.StatusOK, &out); err != nil {
		return RefundsPage{}, err
	}
	page := RefundsPage{NextCursor: out.NextCursor, HasMore: out.HasMore}
	for _, r := range out.Refunds {
		f, err := r.refund()
		if err != nil {
			return RefundsPage{}, err
		}
		page.Refunds = append(page.Refunds, f)
	}
	return page, nil
}

func (c *DictClient) GetRefund(ctx context.Context, id string) (Refund, error) {
	var out refundResponseXML
	if err := c.do(ctx, http.MethodGet, "/refunds/"+url.PathEscape(id), c.medHeader(), nil, http.StatusOK, &out); err != nil {
		return Refund{}, err
	}
	return out.Refund.refund()
}

// CloseRefund gives the wallet's answer to a refund asked of it.
func (c *DictClient) CloseRefund(ctx context.Context, p CloseRefundParams) (Refund, error) {
	body := closeRefundXML{
		ID: p.ID, Participant: c.ispb, Result: p.Result, Details: p.Details, RejectionReason: p.RejectionReason,
		RefundTransactionID: p.RefundTransactionID,
	}
	var out refundResponseXML
	if err := c.do(ctx, http.MethodPost, "/refunds/"+url.PathEscape(p.ID)+"/close", c.medHeader(), body, http.StatusOK, &out); err != nil {
		return Refund{}, err
	}
	return out.Refund.refund()
}

func parseMEDAmount(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	cents, err := parseAmount(s)
	if err != nil {
		return 0, fmt.Errorf("dict: bad amount %q: %w", s, err)
	}
	return cents, nil
}

func parseMEDTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("dict: bad time %q: %w", s, err)
	}
	return t, nil
}
