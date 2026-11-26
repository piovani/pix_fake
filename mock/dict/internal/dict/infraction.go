package dict

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"
)

const (
	maxDetails     = 2000
	defaultListMED = 20
	maxListMED     = 200
)

var (
	reportTransactionRe = regexp.MustCompile(`^\w{8,32}$`) // the InfractionReport schema
	refundTransactionRe = regexp.MustCompile(`^\w{32}$`)   // the Refund schema
	emailRe             = keyRegexps[KeyEmail]
	phoneRe             = keyRegexps[KeyPhone]
	taxIDRe             = regexp.MustCompile(`^([0-9]{11}|[A-Z0-9]{12}[0-9]{2})$`)
)

func (s *Service) stamp() time.Time { return s.now().UTC().Truncate(time.Millisecond) }

func validSituation(t SituationType) bool {
	switch t {
	case SituationScam, SituationAccountTakeover, SituationCoercion, SituationFraudulentAccess, SituationOther:
		return true
	}
	return false
}

func validateDetails(property, details string, v *violations) {
	if utf8.RuneCountInString(details) > maxDetails {
		v.add(property, "", fmt.Sprintf("Value exceeds %d characters", maxDetails))
	}
}

func validateContact(prefix string, c *Contact, v *violations) {
	if c == nil {
		return
	}
	validateRegexp(prefix+".email", c.Email, emailRe, v)
	validateRegexp(prefix+".phone", c.Phone, phoneRe, v)
}

func validateSituation(prefix string, t SituationType, details string, v *violations) {
	if !validSituation(t) {
		v.add(prefix+".situationType", string(t), "Invalid situation type, valid values: SCAM, ACCOUNT_TAKEOVER, COERCION, FRAUDULENT_ACCESS, OTHER")
	}
	if t == SituationOther && details == "" {
		v.add(prefix+".reportDetails", "", "Value is required when SituationType is OTHER")
	}
	validateDetails(prefix+".reportDetails", details, v)
}

func reportNotFound() error { return newError(KindNotFound, "Infraction report not found") }

// partyOf returns the report, or NotFound when the caller is neither of its participants: a participant does not
// learn of reports it is not part of.
func partyOf(r InfractionReport, err error, caller string) (InfractionReport, error) {
	if errors.Is(err, ErrNotFound) || err == nil && caller != r.Reporter && caller != r.Counterparty {
		return InfractionReport{}, reportNotFound()
	}
	return r, err
}

type CreateReportInput struct {
	Participant   string
	TransactionID string
	Reason        InfractionReason
	Situation     SituationType
	ReportDetails string
	Contact       *Contact
}

// CreateInfractionReport opens a report on a transaction. For REFUND_REQUEST the reporter is the payer's participant
// and the counterparty the payee's; for REFUND_CANCELLED it is the other way round. One report per transaction and
// reason can be open at a time.
func (s *Service) CreateInfractionReport(ctx context.Context, caller string, in CreateReportInput) (InfractionReport, error) {
	var v violations
	validateRegexp("participant", in.Participant, ispbRe, &v)
	validateRegexp("infractionReport.transactionId", in.TransactionID, reportTransactionRe, &v)
	switch in.Reason {
	case ReasonRefundRequest:
	case ReasonRefundCancelled:
		if in.Situation != SituationOther {
			v.add("infractionReport.situationType", string(in.Situation), "SituationType must be OTHER when Reason is REFUND_CANCELLED")
		}
	default:
		v.add("infractionReport.reason", string(in.Reason), "Invalid reason, valid values: REFUND_REQUEST, REFUND_CANCELLED")
	}
	validateSituation("infractionReport", in.Situation, in.ReportDetails, &v)
	validateContact("infractionReport.contactInformation", in.Contact, &v)
	if len(v) > 0 {
		return InfractionReport{}, invalid(KindReportInvalid, "InfractionReport has invalid fields", v)
	}
	if in.Participant != caller {
		return InfractionReport{}, newError(KindForbidden, "Participant is not allowed to create infraction reports for another participant")
	}

	var out InfractionReport
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		t, err := tx.Transaction(ctx, in.TransactionID)
		if errors.Is(err, ErrNotFound) {
			v.add("infractionReport.transactionId", in.TransactionID, "Transaction was not settled in the SPI")
			return invalid(KindReportInvalid, "InfractionReport has invalid fields", v)
		}
		if err != nil {
			return err
		}
		reporter, counterparty := t.DebtorParticipant, t.CreditorParticipant
		if in.Reason == ReasonRefundCancelled {
			reporter, counterparty = counterparty, reporter
		}
		if caller != reporter {
			return newError(KindForbidden, fmt.Sprintf("Only the participant of the %s can report this transaction for %s",
				map[bool]string{true: "payer", false: "payee"}[in.Reason == ReasonRefundRequest], in.Reason))
		}
		reports, err := tx.ReportsOf(ctx, t.ID)
		if err != nil {
			return err
		}
		for _, r := range reports {
			if r.Reason == in.Reason && (r.Status == ReportOpen || r.Status == ReportAcknowledged) {
				return newError(KindConflict, "There is already an infraction report in progress for this transaction")
			}
		}
		now := s.stamp()
		out = InfractionReport{
			ID: s.newID(), TransactionID: t.ID, Reason: in.Reason, Situation: in.Situation, ReportDetails: in.ReportDetails,
			Contact: in.Contact, Status: ReportOpen, Reporter: reporter, Counterparty: counterparty, Amount: t.Amount,
			CreditorTaxID: t.CreditorTaxID, CreditorKey: t.CreditorKey, Created: now, Modified: now,
		}
		return tx.SaveReport(ctx, out)
	})
	return out, err
}

// GetInfractionReport returns a report the caller is part of.
func (s *Service) GetInfractionReport(ctx context.Context, caller, id string) (InfractionReport, error) {
	var out InfractionReport
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		r, err := tx.Report(ctx, id)
		out, err = partyOf(r, err, caller)
		return err
	})
	return out, err
}

// mutateReport runs fn on a report the caller is part of, inside one transaction, and stores what fn left.
func (s *Service) mutateReport(ctx context.Context, caller, id string, fn func(tx MEDTx, r *InfractionReport) (changed bool, err error)) (InfractionReport, error) {
	var out InfractionReport
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		r, err := tx.Report(ctx, id)
		if r, err = partyOf(r, err, caller); err != nil {
			return err
		}
		changed, err := fn(tx, &r)
		if err != nil {
			return err
		}
		if changed {
			r.Modified = s.stamp()
			if err := tx.SaveReport(ctx, r); err != nil {
				return err
			}
		}
		out = r
		return nil
	})
	return out, err
}

type UpdateReportInput struct {
	Participant   string
	Situation     SituationType // empty: unchanged
	ReportDetails *string
	Contact       *Contact
}

// UpdateInfractionReport changes the situation, details and contact of one of the caller's reports, while it is
// OPEN or ACKNOWLEDGED.
func (s *Service) UpdateInfractionReport(ctx context.Context, caller, id string, in UpdateReportInput) (InfractionReport, error) {
	if in.Participant != caller {
		return InfractionReport{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	return s.mutateReport(ctx, caller, id, func(_ MEDTx, r *InfractionReport) (bool, error) {
		if caller != r.Reporter {
			return false, newError(KindForbidden, "Only the participant that created the infraction report can update it")
		}
		if r.Status != ReportOpen && r.Status != ReportAcknowledged {
			return false, newError(KindConflict, "An infraction report can only be updated while OPEN or ACKNOWLEDGED")
		}
		if in.Situation != "" {
			r.Situation = in.Situation
		}
		if in.ReportDetails != nil {
			r.ReportDetails = *in.ReportDetails
		}
		if in.Contact != nil {
			r.Contact = in.Contact
		}
		var v violations
		validateSituation("infractionReport", r.Situation, r.ReportDetails, &v)
		validateContact("contactInformation", in.Contact, &v)
		if r.Reason == ReasonRefundCancelled && r.Situation != SituationOther {
			v.add("situationType", string(r.Situation), "SituationType must be OTHER when Reason is REFUND_CANCELLED")
		}
		if len(v) > 0 {
			return false, invalid(KindReportInvalid, "InfractionReport has invalid fields", v)
		}
		return true, nil
	})
}

// AcknowledgeInfractionReport is the counterparty saying it received the report. Repeating it is harmless.
func (s *Service) AcknowledgeInfractionReport(ctx context.Context, caller, participant, id string) (InfractionReport, error) {
	if participant != caller {
		return InfractionReport{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	return s.mutateReport(ctx, caller, id, func(_ MEDTx, r *InfractionReport) (bool, error) {
		if caller != r.Counterparty {
			return false, newError(KindForbidden, "Only the counterparty of the infraction report can acknowledge it")
		}
		switch r.Status {
		case ReportOpen:
			r.Status = ReportAcknowledged
			return true, nil
		case ReportAcknowledged:
			return false, nil
		default:
			return false, newError(KindConflict, fmt.Sprintf("The infraction report is %s, it can no longer be acknowledged", r.Status))
		}
	})
}

// CancelInfractionReport is the reporter withdrawing its report, at any moment. Cancelling a report closed with AGREED
// also cancels the fraud marker it created. Repeating it is harmless.
func (s *Service) CancelInfractionReport(ctx context.Context, caller, participant, id string) (InfractionReport, error) {
	if participant != caller {
		return InfractionReport{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	return s.mutateReport(ctx, caller, id, func(tx MEDTx, r *InfractionReport) (bool, error) {
		if caller != r.Reporter {
			return false, newError(KindForbidden, "Only the participant that created the infraction report can cancel it")
		}
		if r.Status == ReportCancelled {
			return false, nil
		}
		if r.FraudMarkerID != "" {
			m, err := tx.Marker(ctx, r.FraudMarkerID)
			if err != nil {
				return false, err
			}
			if m.Status != MarkerCancelled {
				m.Status, m.Modified = MarkerCancelled, s.stamp()
				if err := tx.SaveMarker(ctx, m); err != nil {
					return false, err
				}
			}
		}
		r.Status = ReportCancelled
		return true, nil
	})
}

type CloseReportInput struct {
	Participant     string
	Result          AnalysisResult
	FraudType       FraudType
	AnalysisDetails string
}

// CloseInfractionReport is the counterparty's analysis of an ACKNOWLEDGED report. AGREED creates a fraud marker on the
// payee of the transaction (the user the report is about), registered by the counterparty. Closing again with the same
// analysis is harmless; with another one it is a conflict.
func (s *Service) CloseInfractionReport(ctx context.Context, caller, id string, in CloseReportInput) (InfractionReport, error) {
	var v violations
	switch in.Result {
	case AnalysisAgreed:
		if !slices.Contains([]FraudType{FraudApplication, FraudMule, FraudScammer, FraudOther}, in.FraudType) {
			v.add("fraudType", string(in.FraudType), "FraudType is required when AnalysisResult is AGREED, valid values: APPLICATION_FRAUD, MULE_ACCOUNT, SCAMMER_ACCOUNT, OTHER")
		}
		if in.FraudType == FraudOther && in.AnalysisDetails == "" {
			v.add("analysisDetails", "", "Value is required when FraudType is OTHER")
		}
	case AnalysisDisagreed:
		if in.FraudType != "" {
			v.add("fraudType", string(in.FraudType), "FraudType is only allowed when AnalysisResult is AGREED")
		}
	default:
		v.add("analysisResult", string(in.Result), "Invalid analysis result, valid values: AGREED, DISAGREED")
	}
	validateDetails("analysisDetails", in.AnalysisDetails, &v)
	if len(v) > 0 {
		return InfractionReport{}, invalid(KindReportInvalid, "InfractionReport has invalid fields", v)
	}
	if in.Participant != caller {
		return InfractionReport{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	return s.mutateReport(ctx, caller, id, func(tx MEDTx, r *InfractionReport) (bool, error) {
		if caller != r.Counterparty {
			return false, newError(KindForbidden, "Only the counterparty of the infraction report can close it")
		}
		switch r.Status {
		case ReportClosed:
			if r.AnalysisResult == in.Result && r.FraudType == in.FraudType && r.AnalysisDetails == in.AnalysisDetails {
				return false, nil
			}
			return false, newError(KindConflict, "The infraction report was already closed with another analysis")
		case ReportAcknowledged:
		default:
			return false, newError(KindConflict, fmt.Sprintf("The infraction report is %s: only an ACKNOWLEDGED report can be closed", r.Status))
		}
		r.Status, r.AnalysisResult, r.FraudType, r.AnalysisDetails = ReportClosed, in.Result, in.FraudType, in.AnalysisDetails
		if in.Result == AnalysisAgreed {
			now := s.stamp()
			m := FraudMarker{
				ID: s.newID(), Registrant: r.Counterparty, TaxID: r.CreditorTaxID, Key: r.CreditorKey, FraudType: in.FraudType,
				Status: MarkerRegistered, ReportID: r.ID, Reporter: r.Reporter, Amount: r.Amount, Created: now, Modified: now,
			}
			if r.Reason == ReasonRefundCancelled { // the report is about the payer of the transaction
				t, err := tx.Transaction(ctx, r.TransactionID)
				if err != nil {
					return false, err
				}
				m.TaxID, m.Key = t.DebtorTaxID, ""
			}
			if err := tx.SaveMarker(ctx, m); err != nil {
				return false, err
			}
			r.FraudMarkerID = m.ID
		}
		return true, nil
	})
}

type ListReportsInput struct {
	Participant    string
	IsReporter     *bool
	IsCounterparty *bool
	Status         []ReportStatus
	ModifiedAfter  time.Time // zero for no bound
	ModifiedBefore time.Time
	Limit          int // 0 for the default
}

// ListInfractionReports lists the reports the caller is part of, oldest change first.
func (s *Service) ListInfractionReports(ctx context.Context, caller string, in ListReportsInput) ([]InfractionReport, bool, error) {
	var v violations
	validateRegexp("Participant", in.Participant, ispbRe, &v)
	for _, st := range in.Status {
		if !slices.Contains([]ReportStatus{ReportOpen, ReportAcknowledged, ReportClosed, ReportCancelled}, st) {
			v.add("Status", string(st), "Invalid status, valid values: OPEN, ACKNOWLEDGED, CLOSED, CANCELLED")
		}
	}
	limit := listLimit(in.Limit, &v)
	if len(v) > 0 {
		return nil, false, invalid(KindBadRequest, "Request has invalid parameters", v)
	}
	if in.Participant != caller {
		return nil, false, newError(KindForbidden, "Participant is not allowed to list the infraction reports of another participant")
	}
	all, err := s.repo.ListReports(ctx, ReportQuery{Participant: caller})
	if err != nil {
		return nil, false, err
	}
	var out []InfractionReport
	for _, r := range all {
		if in.IsReporter != nil && *in.IsReporter != (r.Reporter == caller) ||
			in.IsCounterparty != nil && *in.IsCounterparty != (r.Counterparty == caller) ||
			len(in.Status) > 0 && !slices.Contains(in.Status, r.Status) ||
			!within(r.Modified, in.ModifiedAfter, in.ModifiedBefore) {
			continue
		}
		if len(out) == limit {
			return out, true, nil
		}
		out = append(out, r)
	}
	return out, false, nil
}

// within reports whether t is in [after, before], a zero bound being no bound.
func within(t, after, before time.Time) bool {
	return (after.IsZero() || !t.Before(after)) && (before.IsZero() || !t.After(before))
}

// listLimit checks the Limit of a list, 1 to 200, 20 when absent; a bad value is added to v.
func listLimit(limit int, v *violations) int {
	switch {
	case limit == 0:
		return defaultListMED
	case limit < 1 || limit > maxListMED:
		v.add("Limit", fmt.Sprint(limit), "Limit must be between 1 and 200")
	}
	return limit
}
