package payment

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

// Timeouts are the SPI's to decide (AB03): the wallet never rejects on its own a payment the SPI may have taken, or the money could leave twice.
func (s *Service) OnStatus(ctx context.Context, st pix.TxStatus) error {
	isReturn := st.InstrID != "" && st.InstrID != st.EndToEndID
	if isReturn {
		tag(ctx, st.EndToEndID, st.InstrID)
	} else {
		tag(ctx, st.EndToEndID, "")
	}
	key := st.EndToEndID
	if isReturn {
		key = st.InstrID
	}
	sent, found, err := s.store.ByEndToEnd(ctx, key, Outbound)
	if err != nil {
		return err
	}
	if found && (sent.Type == TypePix || sent.Type == TypeReturn) {
		return s.onOutboundStatus(ctx, sent, st)
	}
	received, found, err := s.store.ByEndToEnd(ctx, key, Inbound)
	if err != nil {
		return err
	}
	wanted := TypePix
	if isReturn {
		wanted = TypeReturn
	}
	if found && received.Type == wanted {
		return s.onInboundStatus(ctx, received, st)
	}
	return nil
}

func (s *Service) onOutboundStatus(ctx context.Context, p Payment, st pix.TxStatus) error {
	switch st.Status {
	case pix.StatusSettled:
		return s.settle(ctx, p, st.SettledAt)
	case pix.StatusRejected:
		rej := st.Rejection()
		if rej.Alert() {
			slog.ErrorContext(ctx, "payment rejected, check reason", "payment", p.ID, "code", rej.Code, "detail", st.Detail)
		}
		_, err := s.reject(ctx, p, rej)
		return err
	}
	return nil
}

// The SPI did nothing with the message, so the payment it names never entered the SPI and is rejected.
func (s *Service) OnNotification(ctx context.Context, n pix.Notification) error {
	p, found, err := s.store.ByResource(ctx, n.Ref)
	if err != nil {
		return err
	}
	if !found {
		slog.WarnContext(ctx, "admi.002 for unknown message", "ref", n.Ref, "reason", n.Reason)
		return nil
	}
	slog.ErrorContext(ctx, "spi could not process order", "payment", p.ID, "reason", n.Reason, "description", n.Description)
	_, err = s.reject(ctx, p, n.Rejection())
	return err
}

func (s *Service) settle(ctx context.Context, p Payment, at time.Time) error {
	if p.Status == Rejected {
		slog.ErrorContext(ctx, "settled after reject", "payment", p.ID, "end_to_end_id", p.EndToEndID)
		return nil
	}
	if err := s.accounts.Settle(ctx, p.ReservationID); err != nil {
		if errors.Is(err, ledger.ErrReservationClosed) {
			slog.ErrorContext(ctx, "settled but reservation released", "payment", p.ID, "end_to_end_id", p.EndToEndID)
			return nil
		}
		return err
	}
	if at.IsZero() {
		at = s.now()
	}
	return s.close(ctx, p, Settled, "", "", at)
}

func (s *Service) reject(ctx context.Context, p Payment, rej pix.Rejection) (Payment, error) {
	if p.Status == Settled {
		slog.ErrorContext(ctx, "rejected after settle", "payment", p.ID, "code", rej.Code)
		return p, nil
	}
	if err := s.accounts.Release(ctx, p.ReservationID); err != nil {
		if errors.Is(err, ledger.ErrReservationClosed) {
			slog.ErrorContext(ctx, "rejected but reservation settled", "payment", p.ID, "code", rej.Code)
			return p, nil
		}
		return p, err
	}
	at := s.now()
	if err := s.close(ctx, p, Rejected, rej.Code, rej.Message, at); err != nil {
		return p, err
	}
	p.Status, p.RejectCode, p.RejectReason, p.FinishedAt = Rejected, rej.Code, rej.Message, at
	return p, nil
}

// The SPI is idempotent by EndToEndId, so an order that had in fact arrived gets the outcome it had.
func (s *Service) SendPending(ctx context.Context) (int, error) {
	unsent, err := s.store.Unsent(ctx, s.now().Add(-resubmitAfter), 50)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, p := range unsent {
		after, err := s.submit(ctx, p)
		if err != nil {
			return sent, err
		}
		if after.Submitted() {
			sent++
		}
	}
	return sent, nil
}

// An unreadable message can never succeed, so it is skipped; an error means it should be delivered again.
func (s *Service) Handle(ctx context.Context, m pix.Message) (err error) {
	kind, kerr := pix.MessageKind(m.Body)
	ctx, span := tracer.Start(ctx, "spi.handle "+kind, trace.WithAttributes(attribute.String("spi.resource_id", m.ResourceID)))
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()
	err = kerr
	if err != nil {
		slog.ErrorContext(ctx, "skip unreadable message", "resource", m.ResourceID, "err", err)
		return nil
	}
	switch kind {
	case pix.KindPacs002:
		sts, err := pix.ParsePacs002(m.Body)
		if err != nil {
			slog.ErrorContext(ctx, "skip bad pacs.002", "resource", m.ResourceID, "err", err)
			return nil
		}
		for _, st := range sts {
			if err := s.OnStatus(ctx, st); err != nil {
				return err
			}
		}
	case pix.KindPacs008:
		orders, err := pix.ParsePacs008(m.Body)
		if err != nil {
			slog.ErrorContext(ctx, "skip bad pacs.008", "resource", m.ResourceID, "err", err)
			return nil
		}
		for _, o := range orders {
			if err := s.OnPaymentOrder(ctx, o); err != nil {
				return err
			}
		}
	case pix.KindPacs004:
		returns, err := pix.ParsePacs004(m.Body)
		if err != nil {
			slog.ErrorContext(ctx, "skip bad pacs.004", "resource", m.ResourceID, "err", err)
			return nil
		}
		for _, r := range returns {
			if err := s.OnReturnOrder(ctx, r); err != nil {
				return err
			}
		}
	case pix.KindCamt025:
		receipts, err := pix.ParseCamt025(m.Body)
		if err != nil {
			slog.ErrorContext(ctx, "skip bad camt.025", "resource", m.ResourceID, "err", err)
			return nil
		}
		for _, r := range receipts {
			if err := s.OnReceipt(ctx, r); err != nil {
				return err
			}
		}
	case pix.KindAdmi002:
		n, err := pix.ParseAdmi002(m.Body)
		if err != nil {
			slog.ErrorContext(ctx, "skip bad admi.002", "resource", m.ResourceID, "err", err)
			return nil
		}
		return s.OnNotification(ctx, n)
	default:
		slog.WarnContext(ctx, "skip unhandled message", "kind", kind, "resource", m.ResourceID)
	}
	return nil
}

type Stream interface {
	Pull(ctx context.Context, next string) ([]pix.Message, string, error)
	Ack(ctx context.Context, next string) error
}

const retryDelay = time.Second

// A batch is acknowledged only after all of it was applied; a failed one is delivered again, and applying twice is safe.
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
			slog.WarnContext(ctx, "pull failed, retrying", "err", err)
			next = ""
			sleep(ctx, retryDelay)
			continue
		}
		if len(msgs) == 0 {
			next = following
			continue
		}

		if err := s.handleAll(ctx, msgs); err != nil {
			slog.ErrorContext(ctx, "batch failed, not acked", "err", err)
			next = ""
			sleep(ctx, retryDelay)
			continue
		}
		if err := stream.Ack(ctx, following); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "ack failed", "err", err)
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
