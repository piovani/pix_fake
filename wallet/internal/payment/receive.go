package payment

import (
	"context"
	"errors"
	"log/slog"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

// OnPaymentOrder answers a pacs.008 sent to the wallet. An accepted order is recorded as pending and answered ACSP; the money
// is credited only when the SPI settles it (ACCC). A refused one is only answered: no money moved, so nothing is recorded.
func (s *Service) OnPaymentOrder(ctx context.Context, o pix.PaymentOrder) error {
	acct, reason, err := s.receiverOf(ctx, o)
	if err != nil {
		return err
	}
	if reason != "" {
		return s.answer(ctx, o.EndToEndID, pix.StatusRejected, reason)
	}

	stored, created, err := s.store.Insert(ctx, Payment{
		AccountID: acct.ID, Direction: Inbound, Type: TypePix, Status: Pending, AmountCents: o.AmountCents,
		EndToEndID: o.EndToEndID, Counterparty: payerOf(o), Message: o.Message, CreatedAt: s.now(),
	})
	if err != nil {
		return err
	}
	if !created && stored.Status != Pending {
		return nil
	}
	return s.answer(ctx, o.EndToEndID, pix.StatusAccepted, "")
}

func (s *Service) receiverOf(ctx context.Context, o pix.PaymentOrder) (ledger.Account, string, error) {
	acct, err := s.accounts.AccountByNumber(ctx, o.Receiver.Branch, o.Receiver.Account)
	switch {
	case errors.Is(err, ledger.ErrAccountNotFound):
		return ledger.Account{}, "AC03", nil
	case err != nil:
		return ledger.Account{}, "", err
	case acct.Status == ledger.Closed:
		return acct, "AC07", nil
	case acct.Status == ledger.Blocked:
		return acct, "AC06", nil
	case acct.TaxID != o.Receiver.TaxID:
		return acct, "BE01", nil
	case acct.Type != string(o.Receiver.AccountType):
		return acct, "AC14", nil
	case acct.ReceiveLimitCents > 0 && o.AmountCents > acct.ReceiveLimitCents:
		return acct, "AM02", nil
	}
	return acct, "", nil
}

func payerOf(o pix.PaymentOrder) Payee {
	return Payee{
		Name: o.Payer.Name, TaxID: o.Payer.TaxID, Participant: o.Payer.ISPB, Branch: o.Payer.Branch,
		AccountNumber: o.Payer.Account, AccountType: o.Payer.AccountType,
	}
}

func (s *Service) answer(ctx context.Context, endToEndID, status, reason string) error {
	_, body, err := pix.NewPacs002(s.ispb, pix.TxStatus{EndToEndID: endToEndID, Status: status, ReasonCode: reason}, s.now())
	if err != nil {
		return err
	}
	_, err = s.bank.Send(ctx, body)
	if errors.Is(err, pix.ErrBadRequest) {
		slog.ErrorContext(ctx, "answer refused by spi", "end_to_end_id", endToEndID, "err", err)
		return nil
	}
	return err
}

func (s *Service) onInboundStatus(ctx context.Context, p Payment, st pix.TxStatus) error {
	switch st.Status {
	case pix.StatusCredit:
		if p.Status == Rejected {
			slog.ErrorContext(ctx, "inbound settled after reject", "payment", p.ID, "end_to_end_id", p.EndToEndID)
			return nil
		}
		if err := s.accounts.Credit(ctx, p.AccountID, p.AmountCents, p.EndToEndID); err != nil {
			return err
		}
		at := st.SettledAt
		if at.IsZero() {
			at = s.now()
		}
		return s.close(ctx, p, Settled, "", "", at)
	case pix.StatusRejected:
		if p.Status == Settled {
			slog.ErrorContext(ctx, "inbound rejected after settle", "payment", p.ID, "code", st.ReasonCode)
			return nil
		}
		rej := st.Rejection()
		return s.close(ctx, p, Rejected, rej.Code, rej.Message, s.now())
	}
	return nil
}
