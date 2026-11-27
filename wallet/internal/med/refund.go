package med

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

// The return reason of the SPI for each refund reason of the DICT.
var returnReasons = map[string]string{"FRAUD": pix.ReturnReasonFraud, "OPERATIONAL_FLAW": pix.ReturnReasonOperationalFlaw}

// SyncRefunds records the open refunds asked of the wallet that it has not seen yet; ProcessRefunds answers them.
func (s *Service) SyncRefunds(ctx context.Context) (int, error) {
	n, cursor := 0, ""
	for {
		page, err := s.dict.ListRefunds(ctx, pix.ListRefundsParams{AsContested: true, Statuses: []string{"OPEN"}, Cursor: cursor, Limit: listLimit})
		if err != nil {
			return n, err
		}
		for _, f := range page.Refunds {
			if _, ok := returnReasons[f.Reason]; !ok {
				continue // REFUND_CANCELLED is asked of the wallet as the payer's participant: not here yet
			}
			requestID, err := pix.NewRequestID()
			if err != nil {
				return n, err
			}
			_, created, err := s.store.InsertRefund(ctx, Refund{
				ID: f.ID, TransactionID: f.TransactionID, Reason: f.Reason, AmountCents: f.AmountCents, Status: RefundReturning,
				RequestID: requestID, CreatedAt: s.now(), UpdatedAt: s.now(),
			})
			if err != nil {
				return n, err
			}
			if created {
				n++
			}
		}
		if !page.HasMore || page.NextCursor == "" {
			return n, nil
		}
		cursor = page.NextCursor
	}
}

// ProcessRefunds moves on the refunds the wallet is answering: makes the return of each, and closes the refund in the
// DICT once the return settled. It returns how many it closed.
func (s *Service) ProcessRefunds(ctx context.Context) (int, error) {
	refunds, err := s.store.Returning(ctx, 50)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, f := range refunds {
		done, err := s.processRefund(ctx, f)
		if err != nil {
			if ctx.Err() != nil {
				return closed, err
			}
			slog.WarnContext(ctx, "refund not answered yet", "refund", f.ID, "err", err)
			continue
		}
		if done {
			closed++
		}
	}
	return closed, nil
}

func (s *Service) processRefund(ctx context.Context, f Refund) (bool, error) {
	if f.ReturnPaymentID == "" {
		return s.makeReturn(ctx, f)
	}
	ret, err := s.payments.Get(ctx, f.ReturnPaymentID)
	if err != nil {
		return false, err
	}
	switch ret.Status {
	case payment.Settled:
		result := "TOTALLY_ACCEPTED"
		if ret.AmountCents < f.AmountCents {
			result = "PARTIALLY_ACCEPTED"
		}
		return s.close(ctx, f, pix.CloseRefundParams{ID: f.ID, Result: result, RefundTransactionID: ret.EndToEndID})
	case payment.Rejected:
		slog.WarnContext(ctx, "the return of a refund was rejected", "refund", f.ID, "code", ret.RejectCode)
		return s.close(ctx, f, pix.CloseRefundParams{
			ID: f.ID, Result: "REJECTED", RejectionReason: "OTHER", Details: "The return was rejected in the SPI: " + ret.RejectCode,
		})
	}
	return false, nil // the return is still in the SPI
}

// makeReturn sends the money back: from the block of the agreed report of the transaction, and from what else the
// account has. With nothing to send, the refund is rejected for lack of balance.
func (s *Service) makeReturn(ctx context.Context, f Refund) (bool, error) {
	current, err := s.dict.GetRefund(ctx, f.ID)
	if err != nil {
		return false, err
	}
	if current.Status != "OPEN" { // cancelled by the participant that asked for it
		f.Status, f.Result = RefundClosed, current.Status
		return true, s.saveRefund(ctx, f)
	}

	pay, found, err := s.payments.Received(ctx, f.TransactionID)
	if err != nil {
		return false, err
	}
	if !found || pay.Status != payment.Settled {
		return s.close(ctx, f, pix.CloseRefundParams{ID: f.ID, Result: "REJECTED", RejectionReason: "OTHER", Details: "The wallet did not receive this transaction."})
	}
	f.PaymentID = pay.ID

	report, hasBlock, err := s.store.BlockedFor(ctx, f.TransactionID)
	if err != nil {
		return false, err
	}
	// Once decided, the amount stays: the return may already exist, and asking again with its RequestID finds it.
	if f.ReturnCents == 0 {
		bal, err := s.accounts.Balance(ctx, pay.AccountID)
		if err != nil {
			return false, err
		}
		available := bal.AvailableCents
		if hasBlock {
			available += report.BlockedCents
		}
		cents := min(f.AmountCents, pay.AmountCents-pay.ReturnedCents, available)
		if cents <= 0 {
			return s.close(ctx, f, pix.CloseRefundParams{ID: f.ID, Result: "REJECTED", RejectionReason: "NO_BALANCE"})
		}
		f.ReturnCents = cents
		if err := s.saveRefund(ctx, f); err != nil {
			return false, err
		}
	}

	in := payment.ReturnInput{PaymentID: pay.ID, AmountCents: f.ReturnCents, RequestID: f.RequestID, Reason: returnReasons[f.Reason]}
	if hasBlock {
		in.FromBlock = report.BlockID
	}
	ret, err := s.payments.Return(ctx, in)
	if errors.Is(err, ledger.ErrInsufficientFunds) {
		f.ReturnCents = 0 // the balance changed: work it out again next time
		return false, errors.Join(err, s.saveRefund(ctx, f))
	}
	if err != nil {
		return false, err
	}
	f.ReturnPaymentID = ret.ID
	if err := s.saveRefund(ctx, f); err != nil {
		return false, err
	}
	if hasBlock {
		report.BlockStatus = BlockReturned
		if err := s.saveReport(ctx, report); err != nil {
			return false, err
		}
	}
	slog.InfoContext(ctx, "return made for a refund", "refund", f.ID, "return", ret.EndToEndID, "cents", f.ReturnCents)
	return false, nil
}

func (s *Service) close(ctx context.Context, f Refund, p pix.CloseRefundParams) (bool, error) {
	if _, err := s.dict.CloseRefund(ctx, p); err != nil {
		return false, fmt.Errorf("close refund: %w", err)
	}
	f.Status, f.Result, f.RejectionReason = RefundClosed, p.Result, p.RejectionReason
	refunds.Add(ctx, 1, resultAttr(p.Result))
	slog.InfoContext(ctx, "refund closed", "refund", f.ID, "result", p.Result, "return", p.RefundTransactionID)
	return true, s.saveRefund(ctx, f)
}

func (s *Service) saveRefund(ctx context.Context, f Refund) error {
	f.UpdatedAt = s.now()
	return s.store.SaveRefund(ctx, f)
}
