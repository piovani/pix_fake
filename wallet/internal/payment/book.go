package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

// A book transfer is settled inside the wallet: no DICT, no SPI (which would refuse it with AG12). It is reported afterwards.

const (
	reportRetryAfter = time.Minute
	// one message per report, so a refused one does not take the others with it
	reportBatch = 10
)

// payInside records the payment before moving the money, so a call that stops halfway can be finished.
func (s *Service) payInside(ctx context.Context, res Resolution, in PayInput) (Payment, error) {
	stored, created, err := s.store.Insert(ctx, Payment{
		AccountID: res.AccountID, Direction: Outbound, Type: TypeBookTransfer, Status: Pending, AmountCents: in.AmountCents,
		EndToEndID: res.EndToEndID, ResolutionID: res.ID, Counterparty: res.Payee, Message: in.Message, CreatedAt: s.now(),
	})
	if err != nil {
		return Payment{}, err
	}
	if !created {
		return s.repeat(ctx, stored, in)
	}
	return s.completeBookTransfer(ctx, stored)
}

// completeBookTransfer is idempotent at every step, so it also finishes a transfer that stopped halfway.
func (s *Service) completeBookTransfer(ctx context.Context, p Payment) (Payment, error) {
	res, found, err := s.store.Resolution(ctx, p.ResolutionID)
	if err != nil {
		return p, fmt.Errorf("read the resolution: %w", err)
	}
	if !found {
		return p, fmt.Errorf("the resolution %s of payment %s is gone", p.ResolutionID, p.ID)
	}

	err = s.accounts.Transfer(ctx, p.AccountID, res.PayeeAccountID, p.AmountCents, p.EndToEndID)
	if errors.Is(err, ledger.ErrInsufficientFunds) {
		// nothing moved: forget the attempt, so the resolution can be paid after a deposit
		if derr := s.store.Discard(ctx, p.ID); derr != nil {
			return p, derr
		}
		return Payment{}, err
	}
	if err != nil {
		return p, fmt.Errorf("move the money: %w", err)
	}

	payer, err := s.accounts.Account(ctx, p.AccountID)
	if err != nil {
		return p, fmt.Errorf("read the payer: %w", err)
	}
	at := s.now()
	if _, _, err := s.store.Insert(ctx, Payment{
		AccountID: res.PayeeAccountID, Direction: Inbound, Type: TypeBookTransfer, Status: Settled, AmountCents: p.AmountCents,
		EndToEndID: p.EndToEndID, Counterparty: s.partyOf(payer, ""), Message: p.Message, CreatedAt: p.CreatedAt, FinishedAt: at,
	}); err != nil {
		return p, fmt.Errorf("record the payment the payee receives: %w", err)
	}
	if _, err := s.store.Close(ctx, p.ID, Settled, "", "", at); err != nil {
		return p, fmt.Errorf("close the payment: %w", err)
	}
	p.Status, p.FinishedAt = Settled, at
	return p, nil
}

func (s *Service) CompleteBookTransfers(ctx context.Context) (int, error) {
	pending, err := s.store.PendingBookTransfers(ctx, s.now().Add(-resubmitAfter), 50)
	if err != nil {
		return 0, err
	}
	done := 0
	for _, p := range pending {
		switch _, err := s.completeBookTransfer(ctx, p); {
		case errors.Is(err, ledger.ErrInsufficientFunds):
			slog.Warn("a book transfer that stopped halfway cannot be made any more; forgotten", "payment", p.ID)
		case err != nil:
			return done, err
		default:
			done++
		}
	}
	return done, nil
}

func (s *Service) ReportBookTransfers(ctx context.Context) (int, error) {
	todo, err := s.store.UnreportedBookTransfers(ctx, s.now().Add(-reportRetryAfter), 50)
	if err != nil {
		return 0, err
	}
	sent := 0
	for len(todo) > 0 {
		n := min(reportBatch, len(todo))
		var bodies [][]byte
		var inBatch []Payment
		for _, p := range todo[:n] {
			body, err := s.reportOf(ctx, p)
			if err != nil {
				if errors.Is(err, pix.ErrBadRequest) {
					slog.Error("a book transfer cannot be reported", "payment", p.ID, "err", err)
					if err := s.store.SetReport(ctx, p.ID, ReportRejected, "INVALID", s.now()); err != nil {
						return sent, err
					}
					continue
				}
				return sent, err
			}
			bodies, inBatch = append(bodies, body), append(inBatch, p)
		}
		todo = todo[n:]
		if len(bodies) == 0 {
			continue
		}

		if _, err := s.bank.Send(ctx, bodies...); err != nil {
			slog.Warn("the SPI did not take the reports of book transfers; they are sent again later", "err", err)
			return sent, nil
		}
		at := s.now()
		for _, p := range inBatch {
			if err := s.store.SetReport(ctx, p.ID, ReportSent, "", at); err != nil {
				return sent, err
			}
			sent++
		}
	}
	return sent, nil
}

func (s *Service) reportOf(ctx context.Context, p Payment) ([]byte, error) {
	res, found, err := s.store.Resolution(ctx, p.ResolutionID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: the resolution of payment %s is gone", pix.ErrBadRequest, p.ID)
	}
	payer, err := s.accounts.Account(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	payee, err := s.accounts.Account(ctx, res.PayeeAccountID)
	if err != nil {
		return nil, err
	}
	_, body, err := pix.NewTrck002(s.ispb, []pix.ReportedTx{{
		EndToEndID:  p.EndToEndID,
		AmountCents: p.AmountCents,
		SettledAt:   p.FinishedAt,
		Payer:       pix.Party{ISPB: s.ispb, TaxID: payer.TaxID, Branch: payer.Branch, Account: payer.Number, AccountType: pix.AccountType(payer.Type)},
		Receiver:    pix.Party{ISPB: s.ispb, TaxID: payee.TaxID, Branch: payee.Branch, Account: payee.Number, AccountType: pix.AccountType(payee.Type)},
		Key:         p.Counterparty.Key,
	}}, s.now())
	return body, err
}

// OnReceipt: a refusal worth retrying is sent again later; any other is final and logged, since the wallet built the report.
func (s *Service) OnReceipt(ctx context.Context, r pix.Receipt) error {
	p, found, err := s.store.ByEndToEnd(ctx, r.Ref, Outbound)
	if err != nil {
		return err
	}
	if !found || p.Type != TypeBookTransfer {
		return nil
	}
	if !r.Rejected() {
		return s.store.SetReport(ctx, p.ID, ReportAccepted, "", s.now())
	}

	rej := r.Rejection()
	if rej.Alert() {
		slog.Error("the SPI refused the report of a book transfer", "payment", p.ID, "code", rej.Code, "detail", r.Detail)
	}
	if rej.TryAgain {
		return s.store.SetReport(ctx, p.ID, "", rej.Code, s.now())
	}
	return s.store.SetReport(ctx, p.ID, ReportRejected, rej.Code, s.now())
}
