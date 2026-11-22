package payment

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMoneyHeldForAPaymentThatWasNeverRecordedIsReleased(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	before := f.balance(t)
	if _, err := f.ledger.Reserve(ctx, anaID, 900, "E99000001202611211200orphanxxxx"); err != nil {
		t.Fatal(err)
	}
	paid := f.pay(t, f.resolve(t), 300)

	if n, err := f.svc.ReleaseOrphans(ctx); err != nil || n != 0 {
		t.Fatalf("too early: %d, %v", n, err)
	}
	f.advance(24 * time.Hour)
	n, err := f.svc.ReleaseOrphans(ctx)
	if err != nil || n != 1 {
		t.Fatalf("released %d, %v", n, err)
	}
	if got := f.balance(t); got.AvailableCents != before.AvailableCents-300 || got.ReservedCents != 300 {
		t.Fatalf("only the payment keeps its money: %+v, was %+v", got, before)
	}
	if p, _ := f.svc.Get(ctx, paid.ID); p.Status != Pending {
		t.Fatalf("the recorded payment is untouched: %+v", p)
	}
	if n, err := f.svc.ReleaseOrphans(ctx); err != nil || n != 0 {
		t.Fatalf("again: %d, %v", n, err)
	}
}

func TestARequestWhoseMoneyWasReleasedAsAnOrphanCannotBeUsed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)
	id := requestID(t)
	if _, err := f.ledger.Reserve(ctx, anaID, 500, id); err != nil {
		t.Fatal(err)
	}
	f.advance(24 * time.Hour)
	if n, err := f.svc.ReleaseOrphans(ctx); err != nil || n != 1 {
		t.Fatalf("released %d, %v", n, err)
	}
	before := f.balance(t)

	_, err := f.svc.Return(ctx, ReturnInput{PaymentID: orig.ID, AmountCents: 500, RequestID: id})
	if !errors.Is(err, ErrRequestConflict) {
		t.Fatalf("got %v", err)
	}
	if got := f.balance(t); got != before {
		t.Fatalf("balance %+v, was %+v", got, before)
	}
}
