package ledger_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/platform/db/dbtest"
)

func newLedger(t *testing.T) *ledger.Ledger {
	t.Helper()
	return ledger.New(dbtest.NewPool(t))
}

// account opens an account with the given available balance.
func account(t *testing.T, l *ledger.Ledger, number string, cents int64) string {
	t.Helper()
	a, err := l.Open(context.Background(), ledger.Account{Branch: "0001", Number: number, HolderName: "Ana", TaxID: "52998224725"})
	if err != nil {
		t.Fatal(err)
	}
	if cents > 0 {
		if err := l.Credit(context.Background(), a.ID, cents, "deposit-"+number); err != nil {
			t.Fatal(err)
		}
	}
	return a.ID
}

func balance(t *testing.T, l *ledger.Ledger, id string) ledger.Balance {
	t.Helper()
	b, err := l.Balance(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpenAndBalance(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 0)

	if b := balance(t, l, id); b != (ledger.Balance{}) {
		t.Fatalf("new account: %+v", b)
	}
	if _, err := l.Open(ctx, ledger.Account{Branch: "0001", Number: "001"}); !errors.Is(err, ledger.ErrAccountExists) {
		t.Fatalf("same branch and number: %v", err)
	}
	if _, err := l.Open(ctx, ledger.Account{ID: id, Branch: "0001", Number: "002"}); !errors.Is(err, ledger.ErrAccountExists) {
		t.Fatalf("same id: %v", err)
	}
	if _, err := l.Balance(ctx, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := l.Balance(ctx, "not-a-uuid"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("invalid id: %v", err)
	}
}

func TestCreditIsIdempotentByReference(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 0)

	for range 3 {
		if err := l.Credit(ctx, id, 2500, "E99000002202611181200abcdefghijk"); err != nil {
			t.Fatal(err)
		}
	}
	if b := balance(t, l, id); b.AvailableCents != 2500 {
		t.Fatalf("credited three times with one reference: %+v", b)
	}
	if err := l.Credit(ctx, id, 0, "x"); !errors.Is(err, ledger.ErrInvalidAmount) {
		t.Fatalf("zero amount: %v", err)
	}
	if err := l.Credit(ctx, id, 100, ""); !errors.Is(err, ledger.ErrInvalidReference) {
		t.Fatalf("no reference: %v", err)
	}
	if err := l.Credit(ctx, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01", 100, "y"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
}

func TestReserveSettle(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 10000)

	r, err := l.Reserve(ctx, id, 4000, "pay-1")
	if err != nil || r.Status != ledger.Held || r.AmountCents != 4000 {
		t.Fatalf("reserve: %+v, %v", r, err)
	}
	if b := balance(t, l, id); b != (ledger.Balance{AvailableCents: 6000, ReservedCents: 4000}) {
		t.Fatalf("after reserve: %+v", b)
	}

	if err := l.Settle(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.Settle(ctx, r.ID); err != nil { // idempotent
		t.Fatalf("settling twice: %v", err)
	}
	if b := balance(t, l, id); b != (ledger.Balance{AvailableCents: 6000, ReservedCents: 0}) {
		t.Fatalf("after settle: the money should be gone: %+v", b)
	}
	if err := l.Release(ctx, r.ID); !errors.Is(err, ledger.ErrReservationClosed) {
		t.Fatalf("release after settle: %v", err)
	}
}

func TestReserveRelease(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 10000)

	r, err := l.Reserve(ctx, id, 10000, "pay-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(ctx, r.ID); err != nil { // idempotent
		t.Fatalf("releasing twice: %v", err)
	}
	if b := balance(t, l, id); b != (ledger.Balance{AvailableCents: 10000}) {
		t.Fatalf("after release: the money should be back: %+v", b)
	}
	if err := l.Settle(ctx, r.ID); !errors.Is(err, ledger.ErrReservationClosed) {
		t.Fatalf("settle after release: %v", err)
	}
	if err := l.Settle(ctx, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01"); !errors.Is(err, ledger.ErrReservationNotFound) {
		t.Fatalf("unknown reservation: %v", err)
	}
}

func TestReserveInsufficientFundsChangesNothing(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 1000)

	if _, err := l.Reserve(ctx, id, 1001, "pay-1"); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("overdraft: %v", err)
	}
	if b := balance(t, l, id); b != (ledger.Balance{AvailableCents: 1000}) {
		t.Fatalf("a refused reservation changed the balance: %+v", b)
	}
	// The failed attempt left no reservation behind, so the same reference can be used once there is money.
	if err := l.Credit(ctx, id, 1, "top-up"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(ctx, id, 1001, "pay-1"); err != nil {
		t.Fatalf("reserve after top-up: %v", err)
	}
}

func TestReserveIsIdempotentByReference(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 10000)
	other := account(t, l, "002", 10000)

	first, err := l.Reserve(ctx, id, 3000, "pay-1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := l.Reserve(ctx, id, 3000, "pay-1")
	if err != nil || again.ID != first.ID {
		t.Fatalf("same request: %+v, %v", again, err)
	}
	if b := balance(t, l, id); b != (ledger.Balance{AvailableCents: 7000, ReservedCents: 3000}) {
		t.Fatalf("reserved twice: %+v", b)
	}
	if _, err := l.Reserve(ctx, id, 3001, "pay-1"); !errors.Is(err, ledger.ErrReferenceConflict) {
		t.Fatalf("other amount: %v", err)
	}
	if _, err := l.Reserve(ctx, other, 3000, "pay-1"); !errors.Is(err, ledger.ErrReferenceConflict) {
		t.Fatalf("other account: %v", err)
	}
	if _, err := l.Reserve(ctx, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01", 1, "pay-2"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
}

// Many payments at once against one balance: exactly as many as the money allows succeed, and the total never moves.
func TestConcurrentReservationsNeverSpendTheSameMoney(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	id := account(t, l, "001", 500)

	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := l.Reserve(ctx, id, 100, fmt.Sprintf("pay-%d", i))
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	ok := 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ledger.ErrInsufficientFunds):
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 5 {
		t.Fatalf("%d reservations succeeded, want 5", ok)
	}
	if b := balance(t, l, id); b != (ledger.Balance{AvailableCents: 0, ReservedCents: 500}) {
		t.Fatalf("balance: %+v", b)
	}
}

func TestAccountReturnsItsData(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)

	opened, err := l.Open(ctx, ledger.Account{Branch: "0001", Number: "777", HolderName: "Bruno", TaxID: "11144477735", Type: "SVGS"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := l.Account(ctx, opened.ID)
	if err != nil || got.Number != "777" || got.HolderName != "Bruno" || got.TaxID != "11144477735" || got.Type != "SVGS" || got.OpenedAt.IsZero() {
		t.Fatalf("account: %+v, %v", got, err)
	}

	plain, _ := l.Open(ctx, ledger.Account{Branch: "0001", Number: "778", HolderName: "Ana", TaxID: "52998224725"})
	if plain.Type != "CACC" {
		t.Fatalf("default type = %q", plain.Type)
	}
	if _, err := l.Account(ctx, "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	if _, err := l.Account(ctx, "not-a-uuid"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Fatalf("bad id: %v", err)
	}
}
