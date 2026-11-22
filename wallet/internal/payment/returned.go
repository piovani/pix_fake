package payment

import (
	"context"
	"errors"
	"log/slog"

	"pixfake/wallet/internal/pix"
)

// OnReturnOrder answers a pacs.004 sent to the wallet: money coming back for a payment it made. An accepted one is recorded
// as pending and answered ACSP; the payer is credited when the SPI settles it (ACCC).
func (s *Service) OnReturnOrder(ctx context.Context, r pix.PaymentReturn) error {
	if known, found, err := s.store.ByEndToEnd(ctx, r.ReturnID, Inbound); err != nil {
		return err
	} else if found {
		if known.Status != Pending {
			return nil
		}
		return s.answerReturn(ctx, r, pix.StatusAccepted, "")
	}

	orig, found, err := s.store.ByEndToEnd(ctx, r.EndToEndID, Outbound)
	if err != nil {
		return err
	}
	if reason := returnRefusal(orig, found, r); reason != "" {
		return s.answerReturn(ctx, r, pix.StatusRejected, reason)
	}

	if _, _, err := s.store.Insert(ctx, Payment{
		AccountID: orig.AccountID, Direction: Inbound, Type: TypeReturn, Status: Pending, AmountCents: r.AmountCents,
		EndToEndID: r.ReturnID, Counterparty: orig.Counterparty, OriginalPaymentID: orig.ID, Message: r.Message, CreatedAt: s.now(),
	}); err != nil {
		return err
	}
	return s.answerReturn(ctx, r, pix.StatusAccepted, "")
}

func returnRefusal(orig Payment, found bool, r pix.PaymentReturn) string {
	switch {
	case !found || orig.Type != TypePix || orig.Status != Settled:
		return "DS04"
	case r.From != orig.Counterparty.Participant:
		return "DS04"
	case r.AmountCents > orig.AmountCents-orig.ReturnedCents:
		return "AM09"
	}
	return ""
}

func (s *Service) answerReturn(ctx context.Context, r pix.PaymentReturn, status, reason string) error {
	_, body, err := pix.NewPacs002(s.ispb, pix.TxStatus{
		InstrID: r.ReturnID, EndToEndID: r.EndToEndID, Status: status, ReasonCode: reason,
	}, s.now())
	if err != nil {
		return err
	}
	_, err = s.bank.Send(ctx, body)
	if errors.Is(err, pix.ErrBadRequest) {
		slog.Error("the SPI refused the answer to a return", "return_id", r.ReturnID, "err", err)
		return nil
	}
	return err
}
