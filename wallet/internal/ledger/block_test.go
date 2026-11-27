package ledger_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"pixfake/wallet/internal/ledger"
)

func TestBlock(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 1000)

	b, err := l.Block(ctx, id, 300, "med-block:1")
	if err != nil || b.AmountCents != 300 || b.Status != ledger.Held {
		t.Fatalf("block = %+v, %v", b, err)
	}
	if got := balance(t, l, id); got != (ledger.Balance{AvailableCents: 700, ReservedCents: 300}) {
		t.Fatalf("balance = %+v", got)
	}
	again, err := l.Block(ctx, id, 300, "med-block:1")
	if err != nil || again.ID != b.ID || balance(t, l, id).ReservedCents != 300 {
		t.Fatalf("block again = %+v, %v", again, err)
	}

	// Only what is available: the rest of the money.
	part, err := l.Block(ctx, id, 5000, "med-block:2")
	if err != nil || part.AmountCents != 700 {
		t.Fatalf("block above the balance = %+v, %v", part, err)
	}
	if _, err := l.Block(ctx, id, 100, "med-block:3"); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("block with nothing available: %v", err)
	}

	if err := l.Release(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, l, id); got != (ledger.Balance{AvailableCents: 300, ReservedCents: 700}) {
		t.Fatalf("after release = %+v", got)
	}
}

func TestBlockHoldsOnABlockedAccount(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 1000)
	if err := l.SetStatus(ctx, id, ledger.Blocked); err != nil {
		t.Fatal(err)
	}
	if b, err := l.Block(ctx, id, 400, "med-block:1"); err != nil || b.AmountCents != 400 {
		t.Fatalf("block = %+v, %v", b, err)
	}
	if _, err := l.Block(ctx, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01", 1, "med-block:2"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
}

func TestBlockIsNoOrphan(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 1000)
	if _, err := l.Block(ctx, id, 100, "med-block:1"); err != nil {
		t.Fatal(err)
	}
	rsv, err := l.Reserve(ctx, id, 100, "payment-1")
	if err != nil {
		t.Fatal(err)
	}
	held, err := l.HeldBefore(ctx, time.Now().Add(time.Hour), 10)
	if err != nil || len(held) != 1 || held[0].ID != rsv.ID {
		t.Fatalf("held = %+v, %v; want only the reservation of the payment", held, err)
	}
}

func TestReserveFromBlock(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 1000)
	b, err := l.Block(ctx, id, 600, "med-block:1")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.SetStatus(ctx, id, ledger.Blocked); err != nil {
		t.Fatal(err)
	}

	// More than the block takes the rest from what is available; the block is gone.
	r, err := l.ReserveFromBlock(ctx, id, b.ID, 800, "return-1")
	if err != nil || r.AmountCents != 800 || r.Status != ledger.Held {
		t.Fatalf("reserve = %+v, %v", r, err)
	}
	if got := balance(t, l, id); got != (ledger.Balance{AvailableCents: 200, ReservedCents: 800}) {
		t.Fatalf("balance = %+v", got)
	}
	again, err := l.ReserveFromBlock(ctx, id, b.ID, 800, "return-1")
	if err != nil || again.ID != r.ID {
		t.Fatalf("again = %+v, %v", again, err)
	}
	if _, err := l.ReserveFromBlock(ctx, id, b.ID, 900, "return-1"); !errors.Is(err, ledger.ErrReferenceConflict) {
		t.Fatalf("same reference, other amount: %v", err)
	}
	if err := l.Settle(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if got := balance(t, l, id); got != (ledger.Balance{AvailableCents: 200}) {
		t.Fatalf("after settling = %+v", got)
	}
}

func TestReserveFromBlockNotEnough(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 1000)
	b, err := l.Block(ctx, id, 500, "med-block:1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ReserveFromBlock(ctx, id, b.ID, 1500, "return-1"); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("above block and balance: %v", err)
	}
	if got := balance(t, l, id); got != (ledger.Balance{AvailableCents: 500, ReservedCents: 500}) {
		t.Fatalf("the block did not stay: %+v", got)
	}

	other := account(t, l, "002", 1000)
	if _, err := l.ReserveFromBlock(ctx, other, b.ID, 100, "return-2"); !errors.Is(err, ledger.ErrReferenceConflict) {
		t.Fatalf("block of another account: %v", err)
	}
	if _, err := l.ReserveFromBlock(ctx, id, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01", 100, "return-3"); !errors.Is(err, ledger.ErrReservationNotFound) {
		t.Fatalf("unknown block: %v", err)
	}
	// Without a block, from what is available.
	if r, err := l.ReserveFromBlock(ctx, other, "", 1000, "return-4"); err != nil || r.AmountCents != 1000 {
		t.Fatalf("no block = %+v, %v", r, err)
	}
}
