package dict

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

func refundNotFound() error { return newError(KindNotFound, "Refund not found") }

// partyOfRefund returns the refund, or NotFound when the caller is neither of its participants.
func partyOfRefund(r Refund, err error, caller string) (Refund, error) {
	if errors.Is(err, ErrNotFound) || err == nil && caller != r.Requesting && caller != r.Contested {
		return Refund{}, refundNotFound()
	}
	return r, err
}

type CreateRefundInput struct {
	Participant   string
	TransactionID string
	Reason        RefundReason
	Amount        int64 // centavos
	Details       string
}

// refundable is what can still be asked back of a transaction: its amount less what refunds accepted or still open
// already cover.
func refundable(t Transaction, refunds []Refund) int64 {
	left := t.Amount
	for _, r := range refunds {
		if r.Status == RefundOpen || r.Status == RefundClosed && r.AnalysisResult != RefundRejected {
			left -= r.Amount
		}
	}
	return left
}

// CreateRefund asks the payee's participant to send back money of a transaction. Only the payer's participant can.
// FRAUD needs an infraction report of the caller on the transaction, closed with AGREED; OPERATIONAL_FLAW and
// PIX_AUTOMATICO need a description. One refund per transaction can be open at a time, and together they never ask
// for more than the transaction's amount.
func (s *Service) CreateRefund(ctx context.Context, caller string, in CreateRefundInput) (Refund, error) {
	var v violations
	validateRegexp("participant", in.Participant, ispbRe, &v)
	validateRegexp("refund.transactionId", in.TransactionID, refundTransactionRe, &v)
	switch in.Reason {
	case RefundFraud:
	case RefundOperationalFlaw, RefundPixAutomatico:
		if strings.TrimSpace(in.Details) == "" {
			v.add("refund.refundDetails", "", "Value is required when RefundReason is "+string(in.Reason))
		}
	default:
		v.add("refund.refundReason", string(in.Reason), "Invalid refund reason, valid values: FRAUD, OPERATIONAL_FLAW, PIX_AUTOMATICO")
	}
	if in.Amount <= 0 {
		v.add("refund.refundAmount", FormatAmount(in.Amount), "Value must be greater than zero")
	}
	validateDetails("refund.refundDetails", in.Details, &v)
	if len(v) > 0 {
		return Refund{}, invalid(KindBadRequest, "Refund has invalid fields", v)
	}
	if in.Participant != caller {
		return Refund{}, newError(KindForbidden, "Participant is not allowed to create refunds for another participant")
	}

	var out Refund
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		t, err := tx.Transaction(ctx, in.TransactionID)
		if errors.Is(err, ErrNotFound) {
			v.add("refund.transactionId", in.TransactionID, "Transaction was not settled in the SPI")
			return invalid(KindBadRequest, "Refund has invalid fields", v)
		}
		if err != nil {
			return err
		}
		if caller != t.DebtorParticipant {
			return newError(KindForbidden, "Only the participant of the payer can ask for a refund of this transaction")
		}
		var reportID string
		if in.Reason == RefundFraud {
			reports, err := tx.ReportsOf(ctx, t.ID)
			if err != nil {
				return err
			}
			for _, r := range reports {
				if r.Reason == ReasonRefundRequest && r.Reporter == caller && r.Status == ReportClosed && r.AnalysisResult == AnalysisAgreed &&
					r.FundsRecoveryID == "" { // a funds recovery asks for the refunds of its own reports
					reportID = r.ID
				}
			}
			if reportID == "" {
				v.add("refund.refundReason", string(in.Reason), "A refund for FRAUD needs an infraction report on the transaction closed with AGREED")
				return invalid(KindBadRequest, "Refund has invalid fields", v)
			}
		}
		refunds, err := tx.RefundsOf(ctx, t.ID)
		if err != nil {
			return err
		}
		for _, r := range refunds {
			if r.Status == RefundOpen {
				return newError(KindConflict, "There is already an open refund for this transaction")
			}
		}
		if left := refundable(t, refunds); in.Amount > left {
			v.add("refund.refundAmount", FormatAmount(in.Amount), "Value exceeds what can still be refunded of the transaction: "+FormatAmount(left))
			return invalid(KindBadRequest, "Refund has invalid fields", v)
		}
		now := s.stamp()
		out = Refund{
			ID: s.newID(), TransactionID: t.ID, Reason: in.Reason, Amount: in.Amount, Details: in.Details, Status: RefundOpen,
			Requesting: t.DebtorParticipant, Contested: t.CreditorParticipant, InfractionReportID: reportID, Created: now, Modified: now,
		}
		return tx.SaveRefund(ctx, out)
	})
	return out, err
}

// GetRefund returns a refund the caller is part of.
func (s *Service) GetRefund(ctx context.Context, caller, id string) (Refund, error) {
	var out Refund
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		r, err := tx.Refund(ctx, id)
		out, err = partyOfRefund(r, err, caller)
		return err
	})
	return out, err
}

func (s *Service) mutateRefund(ctx context.Context, caller, id string, fn func(tx MEDTx, r *Refund) (changed bool, err error)) (Refund, error) {
	var out Refund
	err := s.repo.InTx(ctx, func(tx MEDTx) error {
		r, err := tx.Refund(ctx, id)
		if r, err = partyOfRefund(r, err, caller); err != nil {
			return err
		}
		changed, err := fn(tx, &r)
		if err != nil {
			return err
		}
		if changed {
			r.Modified = s.stamp()
			if err := tx.SaveRefund(ctx, r); err != nil {
				return err
			}
		}
		out = r
		return nil
	})
	return out, err
}

// CancelRefund is the requesting participant withdrawing an open refund. Repeating it is harmless.
func (s *Service) CancelRefund(ctx context.Context, caller, participant, id string) (Refund, error) {
	if participant != caller {
		return Refund{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	return s.mutateRefund(ctx, caller, id, func(_ MEDTx, r *Refund) (bool, error) {
		if caller != r.Requesting {
			return false, newError(KindForbidden, "Only the participant that asked for the refund can cancel it")
		}
		switch r.Status {
		case RefundCancelled:
			return false, nil
		case RefundClosed:
			return false, newError(KindConflict, "A refund cannot be cancelled after it was closed")
		}
		if r.FundsRecoveryID != "" {
			return false, newError(KindRefundOperation, "The refund belongs to a funds recovery: cancel the funds recovery instead")
		}
		r.Status = RefundCancelled
		return true, nil
	})
}

type CloseRefundInput struct {
	Participant         string
	Result              RefundResult
	AnalysisDetails     string
	RejectionReason     RejectionReason
	RefundTransactionID string // the return (pacs.004) that sent the money back, for an accepted refund
}

// CloseRefund is the contested participant's answer to an open refund. An accepted one carries the id of the return
// (pacs.004) made in the SPI; a rejected one, the reason. Closing again with the same answer is harmless.
func (s *Service) CloseRefund(ctx context.Context, caller, id string, in CloseRefundInput) (Refund, error) {
	var v violations
	switch in.Result {
	case RefundTotallyAccepted, RefundPartiallyAccepted:
		validateRegexp("refundTransactionId", in.RefundTransactionID, refundTransactionRe, &v)
		if in.RejectionReason != "" {
			v.add("refundRejectionReason", string(in.RejectionReason), "RefundRejectionReason is only allowed when RefundAnalysisResult is REJECTED")
		}
	case RefundRejected:
		if !slices.Contains([]RejectionReason{RejectNoBalance, RejectAccountClosure, RejectInvalidRequest, RejectOther}, in.RejectionReason) {
			v.add("refundRejectionReason", string(in.RejectionReason), "RefundRejectionReason is required when RefundAnalysisResult is REJECTED, valid values: NO_BALANCE, ACCOUNT_CLOSURE, INVALID_REQUEST, OTHER")
		}
		if in.RejectionReason == RejectInvalidRequest && strings.TrimSpace(in.AnalysisDetails) == "" {
			v.add("refundAnalysisDetails", "", "Value is required when RefundRejectionReason is INVALID_REQUEST")
		}
		if in.RefundTransactionID != "" {
			v.add("refundTransactionId", in.RefundTransactionID, "A rejected refund has no refund transaction")
		}
	default:
		v.add("refundAnalysisResult", string(in.Result), "Invalid result, valid values: TOTALLY_ACCEPTED, PARTIALLY_ACCEPTED, REJECTED")
	}
	validateDetails("refundAnalysisDetails", in.AnalysisDetails, &v)
	if len(v) > 0 {
		return Refund{}, invalid(KindBadRequest, "Request has invalid fields", v)
	}
	if in.Participant != caller {
		return Refund{}, newError(KindForbidden, "Participant must be the participant making the request")
	}
	return s.mutateRefund(ctx, caller, id, func(tx MEDTx, r *Refund) (bool, error) {
		if caller != r.Contested {
			return false, newError(KindForbidden, "Only the contested participant can close the refund")
		}
		switch r.Status {
		case RefundClosed:
			if r.AnalysisResult == in.Result && r.RejectionReason == in.RejectionReason && r.RefundTransactionID == in.RefundTransactionID &&
				r.AnalysisDetails == in.AnalysisDetails {
				return false, nil
			}
			return false, newError(KindConflict, "The refund was already closed with another answer")
		case RefundCancelled:
			return false, newError(KindConflict, "The refund was cancelled")
		}
		if in.RejectionReason == RejectInvalidRequest && r.Reason != RefundOperationalFlaw {
			return false, invalid(KindBadRequest, "Request has invalid fields", violations{{
				Property: "refundRejectionReason", Value: string(in.RejectionReason),
				Reason: "INVALID_REQUEST can only be used when RefundReason is OPERATIONAL_FLAW",
			}})
		}
		r.Status, r.AnalysisResult, r.AnalysisDetails = RefundClosed, in.Result, in.AnalysisDetails
		r.RejectionReason, r.RefundTransactionID = in.RejectionReason, in.RefundTransactionID
		if r.FundsRecoveryID != "" {
			return true, s.refundClosed(ctx, tx, *r)
		}
		return true, nil
	})
}

type RefundRole string

const (
	RoleRequesting RefundRole = "REQUESTING"
	RoleContested  RefundRole = "CONTESTED"
)

type ListRefundsInput struct {
	Participant    string
	Role           RefundRole
	Status         []RefundStatus
	ModifiedAfter  time.Time
	ModifiedBefore time.Time
	Limit          int
	Cursor         string
}

type RefundsPage struct {
	Refunds    []Refund
	HasMore    bool
	NextCursor string
}

// cursor points after a refund in the order of the list (last change, then id).
func cursorOf(r Refund) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(r.Modified.UnixNano(), 10) + "/" + r.ID))
}

func afterCursor(r Refund, cursor string) (bool, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return false, err
	}
	nanos, id, ok := strings.Cut(string(raw), "/")
	if !ok {
		return false, errors.New("bad cursor")
	}
	n, err := strconv.ParseInt(nanos, 10, 64)
	if err != nil {
		return false, err
	}
	at := r.Modified.UnixNano()
	return at > n || at == n && r.ID > id, nil
}

// ListRefunds lists the refunds in which the caller has the role, oldest change first, in pages of Limit.
func (s *Service) ListRefunds(ctx context.Context, caller string, in ListRefundsInput) (RefundsPage, error) {
	var v violations
	validateRegexp("Participant", in.Participant, ispbRe, &v)
	if in.Role != RoleRequesting && in.Role != RoleContested {
		v.add("ParticipantRole", string(in.Role), "ParticipantRole is required, valid values: REQUESTING, CONTESTED")
	}
	for _, st := range in.Status {
		if !slices.Contains([]RefundStatus{RefundOpen, RefundClosed, RefundCancelled}, st) {
			v.add("Status", string(st), "Invalid status, valid values: OPEN, CLOSED, CANCELLED")
		}
	}
	limit := listLimit(in.Limit, &v)
	if in.Cursor != "" {
		if _, err := afterCursor(Refund{}, in.Cursor); err != nil {
			v.add("Cursor", in.Cursor, "Invalid cursor")
		}
	}
	if len(v) > 0 {
		return RefundsPage{}, invalid(KindBadRequest, "Request has invalid parameters", v)
	}
	if in.Participant != caller {
		return RefundsPage{}, newError(KindForbidden, "Participant is not allowed to list the refunds of another participant")
	}
	all, err := s.repo.ListRefunds(ctx, caller)
	if err != nil {
		return RefundsPage{}, err
	}
	var page RefundsPage
	for _, r := range all {
		mine := r.Requesting
		if in.Role == RoleContested {
			mine = r.Contested
		}
		if mine != caller || len(in.Status) > 0 && !slices.Contains(in.Status, r.Status) || !within(r.Modified, in.ModifiedAfter, in.ModifiedBefore) {
			continue
		}
		if in.Cursor != "" {
			if after, _ := afterCursor(r, in.Cursor); !after {
				continue
			}
		}
		if len(page.Refunds) == limit {
			page.HasMore, page.NextCursor = true, cursorOf(page.Refunds[limit-1])
			break
		}
		page.Refunds = append(page.Refunds, r)
	}
	return page, nil
}

// FormatAmount writes centavos as reais with two decimals, the way the spec writes amounts ("100.00").
func FormatAmount(c int64) string {
	sign := ""
	if c < 0 {
		sign, c = "-", -c
	}
	return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

// ParseAmount reads an amount in reais ("100", "100.5", "100.50") as centavos.
func ParseAmount(s string) (int64, error) {
	s = strings.TrimSpace(s)
	whole, frac, _ := strings.Cut(s, ".")
	if whole == "" || len(frac) > 2 || strings.HasPrefix(whole, "-") || strings.HasPrefix(whole, "+") {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	reais, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	frac += strings.Repeat("0", 2-len(frac))
	cents, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount %q", s)
	}
	return reais*100 + cents, nil
}
