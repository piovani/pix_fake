package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

const returnDeadline = 90 * 24 * time.Hour

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type ReturnInput struct {
	PaymentID   string
	AmountCents int64
	RequestID   string // UUID v4, chosen by the caller
}

// Return sends back, in whole or in part, a payment received through the SPI. The amount is reserved until the SPI answers.
// Repeating a call with the same RequestID returns the same return.
func (s *Service) Return(ctx context.Context, in ReturnInput) (Payment, error) {
	switch {
	case in.PaymentID == "":
		return Payment{}, fmt.Errorf("%w: payment is required", ErrInvalid)
	case in.AmountCents <= 0:
		return Payment{}, fmt.Errorf("%w: amount must be positive", ErrInvalid)
	case !uuidV4.MatchString(in.RequestID):
		return Payment{}, fmt.Errorf("%w: request id must be a UUID v4", ErrInvalid)
	}
	if existing, found, err := s.store.ByRequestID(ctx, in.RequestID); err != nil {
		return Payment{}, err
	} else if found {
		return s.repeatReturn(ctx, existing, in)
	}

	orig, err := s.Get(ctx, in.PaymentID)
	if err != nil {
		return Payment{}, err
	}
	if err := s.checkReturnable(orig, in.AmountCents); err != nil {
		if errors.Is(err, ErrReturnTooLarge) {
			// a call with the same request id may have used up what was left since the lookup above
			if existing, found, rerr := s.store.ByRequestID(ctx, in.RequestID); rerr == nil && found {
				return s.repeatReturn(ctx, existing, in)
			}
		}
		return Payment{}, err
	}

	at := s.now()
	rtrID, err := pix.NewReturnID(s.ispb, at)
	if err != nil {
		return Payment{}, err
	}
	rsv, err := s.accounts.Reserve(ctx, orig.AccountID, in.AmountCents, in.RequestID)
	if errors.Is(err, ledger.ErrReferenceConflict) {
		return Payment{}, ErrRequestConflict
	}
	if err != nil {
		return Payment{}, err
	}

	stored, created, err := s.store.InsertReturn(ctx, Payment{
		AccountID: orig.AccountID, Direction: Outbound, Type: TypeReturn, Status: Pending, AmountCents: in.AmountCents,
		EndToEndID: rtrID, ReservationID: rsv.ID, Counterparty: orig.Counterparty, OriginalPaymentID: orig.ID,
		RequestID: in.RequestID, CreatedAt: at,
	})
	if err != nil {
		if rerr := s.accounts.Release(context.WithoutCancel(ctx), rsv.ID); rerr != nil {
			slog.Error("release after a return that was not stored", "request_id", in.RequestID, "err", rerr)
		}
		return Payment{}, err
	}
	if !created {
		return s.repeatReturn(ctx, stored, in)
	}
	return s.submit(ctx, stored)
}

func (s *Service) checkReturnable(orig Payment, cents int64) error {
	switch {
	case orig.Direction != Inbound || orig.Type != TypePix:
		return fmt.Errorf("%w: only a payment received through the SPI can be returned", ErrNotReturnable)
	case orig.Status != Settled:
		return fmt.Errorf("%w: the payment is not settled", ErrNotReturnable)
	case s.now().Sub(orig.FinishedAt) > returnDeadline:
		return fmt.Errorf("%w: it was settled more than 90 days ago", ErrNotReturnable)
	case cents > orig.AmountCents-orig.ReturnedCents:
		return ErrReturnTooLarge
	}
	return nil
}

func (s *Service) repeatReturn(ctx context.Context, p Payment, in ReturnInput) (Payment, error) {
	if p.OriginalPaymentID != in.PaymentID || p.AmountCents != in.AmountCents {
		return Payment{}, ErrRequestConflict
	}
	if p.Status == Pending && !p.Submitted() {
		return s.submit(ctx, p)
	}
	return p, nil
}

func (s *Service) returnOrderOf(ctx context.Context, p Payment) ([]byte, error) {
	orig, found, err := s.store.ByID(ctx, p.OriginalPaymentID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("the payment %s that %s returns is gone", p.OriginalPaymentID, p.ID)
	}
	_, body, err := pix.NewPacs004(s.ispb, pix.PaymentReturn{
		ReturnID: p.EndToEndID, EndToEndID: orig.EndToEndID, AmountCents: p.AmountCents, To: orig.Counterparty.Participant,
	}, s.now())
	return body, err
}
