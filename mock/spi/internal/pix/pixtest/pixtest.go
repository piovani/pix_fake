// Package pixtest holds the behavior every pix.PaymentStore must have, so the in-memory and
// the Postgres implementations are tested by the same cases.
package pixtest

import (
	"context"
	"errors"
	"testing"
	"time"

	"pixfake/mock/spi/internal/pix"
)

func payment(e2e string) pix.Payment {
	at := time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)
	return pix.Payment{
		EndToEndID: e2e, Status: pix.StatusPending, AmountCents: 12345, Purpose: "IPAY", Initiation: "DICT", Priority: "PAGPRI",
		Info:      "lunch",
		PayerISPB: "99000001", PayerName: "Ana Souza", PayerTaxID: "52998224725", PayerBranch: "0001", PayerAccount: "00123456", PayerAccountType: "CACC",
		PayeeISPB: "99000002", PayeeTaxID: "39053344705", PayeeBranch: "0001", PayeeAccount: "00456789", PayeeAccountType: "CACC", PayeeKey: "39053344705",
		AcceptedAt: at, CreatedAt: at,
	}
}

// norm puts the times in UTC so values read back from a database compare equal.
func norm(p pix.Payment) pix.Payment {
	p.AcceptedAt, p.CreatedAt, p.SettledAt = p.AcceptedAt.UTC(), p.CreatedAt.UTC(), p.SettledAt.UTC()
	return p
}

// Run executes the cases. newStore must return an empty store for each call.
func Run(t *testing.T, newStore func(t *testing.T) pix.PaymentStore) {
	ctx := context.Background()
	const e2e = "E9900000120261114120000000000001"

	t.Run("create and get", func(t *testing.T) {
		s := newStore(t)
		want := payment(e2e)
		got, created, err := s.CreatePayment(ctx, want)
		if err != nil || !created || got.EndToEndID != e2e {
			t.Fatalf("create = %+v, %v, %v", got, created, err)
		}
		read, err := s.GetPayment(ctx, e2e)
		if err != nil {
			t.Fatal(err)
		}
		if norm(read) != norm(want) {
			t.Fatalf("stored = %+v\nwant     %+v", read, want)
		}
	})

	t.Run("create twice keeps the first", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		other := payment(e2e)
		other.AmountCents = 1
		existing, created, err := s.CreatePayment(ctx, other)
		if err != nil || created || existing.AmountCents != 12345 {
			t.Fatalf("second create = %+v, %v, %v; want the existing payment and created == false", existing, created, err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newStore(t)
		if _, err := s.GetPayment(ctx, e2e); !errors.Is(err, pix.ErrPaymentNotFound) {
			t.Fatalf("get = %v", err)
		}
		if _, _, err := s.FinalizePayment(ctx, e2e, pix.StatusSettled, "", "", time.Now()); !errors.Is(err, pix.ErrPaymentNotFound) {
			t.Fatalf("finalize = %v", err)
		}
	})

	t.Run("settle once", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		at := time.Date(2026, 11, 14, 12, 0, 5, 0, time.UTC)
		p, changed, err := s.FinalizePayment(ctx, e2e, pix.StatusSettled, "", "", at)
		if err != nil || !changed || p.Status != pix.StatusSettled || !p.SettledAt.Equal(at) {
			t.Fatalf("settle = %+v, %v, %v", p, changed, err)
		}
		p, changed, err = s.FinalizePayment(ctx, e2e, pix.StatusRejected, "DS04", "late", at)
		if err != nil || changed || p.Status != pix.StatusSettled {
			t.Fatalf("second finalize = %+v, %v, %v; want unchanged and still settled", p, changed, err)
		}
	})

	t.Run("reject keeps the reason", func(t *testing.T) {
		s := newStore(t)
		_, _, _ = s.CreatePayment(ctx, payment(e2e))
		p, changed, err := s.FinalizePayment(ctx, e2e, pix.StatusRejected, "AC03", "no such account", time.Now())
		if err != nil || !changed || p.Status != pix.StatusRejected || p.ReasonCode != "AC03" || p.ReasonDetail != "no such account" || !p.SettledAt.IsZero() {
			t.Fatalf("reject = %+v, %v, %v", p, changed, err)
		}
		read, _ := s.GetPayment(ctx, e2e)
		if read.Status != pix.StatusRejected || read.ReasonCode != "AC03" {
			t.Fatalf("stored = %+v", read)
		}
	})
}
