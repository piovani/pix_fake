package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

// OnStatus applies a pacs.002 from the SPI to the payment it is about: settled (ACSC) takes the reserved money out of
// the account, rejected (RJCT) gives it back, with the reason. It is safe to apply the same status twice. A status that
// is not about a payment this module sent is ignored.
//
// The timeout is the SPI's to decide: a payment nobody answered in time comes back as RJCT with AB03. The wallet never
// rejects on its own a payment the SPI may have taken, or the money could leave twice.
func (s *Service) OnStatus(ctx context.Context, st pix.TxStatus) error {
	if st.InstrID != "" && st.InstrID != st.EndToEndID {
		return nil // the status of a return, not of a payment
	}
	p, found, err := s.store.ByEndToEnd(ctx, st.EndToEndID)
	if err != nil {
		return err
	}
	if !found || p.Direction != Outbound || p.Type != TypePix {
		return nil
	}

	switch st.Status {
	case pix.StatusSettled:
		return s.settle(ctx, p, st.SettledAt)
	case pix.StatusRejected:
		rej := st.Rejection()
		if rej.Alert() {
			slog.Error("payment rejected for a reason that needs attention", "payment", p.ID, "code", rej.Code, "detail", st.Detail)
		}
		_, err := s.reject(ctx, p, rej)
		return err
	}
	return nil
}

// OnNotification applies an admi.002: the SPI could not process a message and did nothing with it, so a payment whose
// order it names never entered the SPI and is rejected.
func (s *Service) OnNotification(ctx context.Context, n pix.Notification) error {
	p, found, err := s.store.ByResource(ctx, n.Ref)
	if err != nil {
		return err
	}
	if !found {
		slog.Warn("the SPI could not process a message that is not a payment of this wallet", "ref", n.Ref, "reason", n.Reason)
		return nil
	}
	slog.Error("the SPI could not process a payment order", "payment", p.ID, "reason", n.Reason, "description", n.Description)
	_, err = s.reject(ctx, p, n.Rejection())
	return err
}

func (s *Service) settle(ctx context.Context, p Payment, at time.Time) error {
	if p.Status == Rejected {
		slog.Error("the SPI settled a payment the wallet had rejected", "payment", p.ID, "end_to_end_id", p.EndToEndID)
		return nil
	}
	if err := s.accounts.Settle(ctx, p.ReservationID); err != nil {
		if errors.Is(err, ledger.ErrReservationClosed) {
			slog.Error("the SPI settled a payment whose reservation was released", "payment", p.ID, "end_to_end_id", p.EndToEndID)
			return nil
		}
		return err
	}
	if at.IsZero() {
		at = s.now()
	}
	_, err := s.store.Close(ctx, p.ID, Settled, "", "", at)
	return err
}

// reject gives the reserved money back and closes the payment. It does nothing to a payment that is not pending.
func (s *Service) reject(ctx context.Context, p Payment, rej pix.Rejection) (Payment, error) {
	if p.Status == Settled {
		slog.Error("a rejection came for a payment already settled", "payment", p.ID, "code", rej.Code)
		return p, nil
	}
	if err := s.accounts.Release(ctx, p.ReservationID); err != nil {
		if errors.Is(err, ledger.ErrReservationClosed) {
			slog.Error("a rejection came for a payment whose reservation was settled", "payment", p.ID, "code", rej.Code)
			return p, nil
		}
		return p, err
	}
	at := s.now()
	if _, err := s.store.Close(ctx, p.ID, Rejected, rej.Code, rej.Message, at); err != nil {
		return p, err
	}
	p.Status, p.RejectCode, p.RejectReason, p.FinishedAt = Rejected, rej.Code, rej.Message, at
	return p, nil
}

// SendPending sends to the SPI the payments it has not accepted yet, such as the ones created while it was unreachable.
// It returns how many it sent. The SPI is idempotent by EndToEndId, so an order that had in fact arrived gets the outcome
// it already had.
func (s *Service) SendPending(ctx context.Context) (int, error) {
	unsent, err := s.store.Unsent(ctx, s.now().Add(-resubmitAfter), 50)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, p := range unsent {
		acct, err := s.accounts.Account(ctx, p.AccountID)
		if err != nil {
			return sent, err
		}
		after, err := s.submit(ctx, p, acct)
		if err != nil {
			return sent, err
		}
		if after.Submitted() {
			sent++
		}
	}
	return sent, nil
}

// Handle applies one message of the SPI stream. A message that cannot be read can never succeed, so it is logged and
// skipped; an error means the message should be delivered again.
//
// Only what answers a payment the wallet sent is handled for now. Received payments (pacs.008), returns (pacs.004) and
// receipts of reports (camt.025) are logged and skipped.
func (s *Service) Handle(ctx context.Context, m pix.Message) error {
	kind, err := pix.MessageKind(m.Body)
	if err != nil {
		slog.Error("unreadable message from the SPI, skipped", "resource", m.ResourceID, "err", err)
		return nil
	}
	switch kind {
	case pix.KindPacs002:
		sts, err := pix.ParsePacs002(m.Body)
		if err != nil {
			slog.Error("unreadable pacs.002, skipped", "resource", m.ResourceID, "err", err)
			return nil
		}
		for _, st := range sts {
			if err := s.OnStatus(ctx, st); err != nil {
				return err
			}
		}
	case pix.KindAdmi002:
		n, err := pix.ParseAdmi002(m.Body)
		if err != nil {
			slog.Error("unreadable admi.002, skipped", "resource", m.ResourceID, "err", err)
			return nil
		}
		return s.OnNotification(ctx, n)
	default:
		slog.Warn("message not handled yet, skipped", "kind", kind, "resource", m.ResourceID)
	}
	return nil
}

// Stream is what Consume needs from the SPI client.
type Stream interface {
	Pull(ctx context.Context, next string) ([]pix.Message, string, error)
	Ack(ctx context.Context, next string) error
}

const retryDelay = time.Second

// Consume reads the SPI stream until ctx ends, applying each message and acknowledging a batch only after all of it was
// applied. A batch that fails is not acknowledged, so the SPI delivers it again; applying a message twice is safe.
func (s *Service) Consume(ctx context.Context, stream Stream) error {
	next := ""
	for ctx.Err() == nil {
		msgs, following, err := stream.Pull(ctx, next)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, pix.ErrStreamGone):
			next = ""
			continue
		case err != nil:
			slog.Warn("reading the SPI stream failed, trying again", "err", err)
			next = ""
			sleep(ctx, retryDelay)
			continue
		}
		if len(msgs) == 0 {
			next = following
			continue
		}

		if err := s.handleAll(ctx, msgs); err != nil {
			slog.Error("applying messages of the SPI failed; they will be delivered again", "err", err)
			next = ""
			sleep(ctx, retryDelay)
			continue
		}
		if err := stream.Ack(ctx, following); err != nil && ctx.Err() == nil {
			slog.Warn("acknowledging the SPI stream failed; the messages will be delivered again", "err", err)
		}
		next = "" // a stream is closed by its acknowledgement; the next batch comes from a new one
	}
	return nil
}

func (s *Service) handleAll(ctx context.Context, msgs []pix.Message) error {
	for _, m := range msgs {
		if err := s.Handle(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
