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

func TestTransferMovesMoneyBetweenAccounts(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	payer, payee := account(t, l, "001", 10000), account(t, l, "002", 500)

	if err := l.Transfer(ctx, payer, payee, 2590, "E-1"); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, l, payer); b != (ledger.Balance{AvailableCents: 7410}) {
		t.Errorf("payer: %+v", b)
	}
	if b := balance(t, l, payee); b != (ledger.Balance{AvailableCents: 3090}) {
		t.Errorf("payee: %+v", b)
	}
}

func TestTransferIsIdempotentByReference(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	payer, payee := account(t, l, "001", 10000), account(t, l, "002", 0)

	for range 3 {
		if err := l.Transfer(ctx, payer, payee, 2590, "E-1"); err != nil {
			t.Fatal(err)
		}
	}
	if b := balance(t, l, payer); b.AvailableCents != 7410 {
		t.Errorf("payer: %+v", b)
	}
	if b := balance(t, l, payee); b.AvailableCents != 2590 {
		t.Errorf("payee: %+v", b)
	}
	if err := l.Transfer(ctx, payer, payee, 9999, "E-1"); !errors.Is(err, ledger.ErrReferenceConflict) {
		t.Errorf("the same reference with another amount: %v", err)
	}
	if err := l.Transfer(ctx, payer, payee, 100, "E-2"); err != nil {
		t.Fatal(err)
	}
	if b := balance(t, l, payee); b.AvailableCents != 2690 {
		t.Errorf("payee: %+v", b)
	}
}

func TestTransferInsufficientFundsChangesNothing(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	payer, payee := account(t, l, "001", 1000), account(t, l, "002", 0)

	if err := l.Transfer(ctx, payer, payee, 1001, "E-1"); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if balance(t, l, payer).AvailableCents != 1000 || balance(t, l, payee).AvailableCents != 0 {
		t.Error("a refused transfer moved money")
	}
	if err := l.Credit(ctx, payer, 1, "top-up"); err != nil {
		t.Fatal(err)
	}
	if err := l.Transfer(ctx, payer, payee, 1001, "E-1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Credit(ctx, payer, 500, "more"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(ctx, payer, 500, "held"); err != nil {
		t.Fatal(err)
	}
	if err := l.Transfer(ctx, payer, payee, 1, "E-3"); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Errorf("transferring reserved money: %v", err)
	}
}

func TestTransferValidation(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	a, b := account(t, l, "001", 1000), account(t, l, "002", 0)
	missing := "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01"

	for name, tt := range map[string]struct {
		from, to string
		cents    int64
		ref      string
		want     error
	}{
		"zero amount":      {a, b, 0, "r", ledger.ErrInvalidAmount},
		"no reference":     {a, b, 1, "", ledger.ErrInvalidReference},
		"the same account": {a, a, 1, "r", ledger.ErrSameAccount},
		"unknown payer":    {missing, b, 1, "r", ledger.ErrAccountNotFound},
		"unknown payee":    {a, missing, 1, "r", ledger.ErrAccountNotFound},
		"malformed payee":  {a, "not-a-uuid", 1, "r", ledger.ErrAccountNotFound},
	} {
		if err := l.Transfer(ctx, tt.from, tt.to, tt.cents, tt.ref); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if balance(t, l, a).AvailableCents != 1000 {
		t.Error("a refused transfer moved money")
	}
}

func TestConcurrentTransfersNeverSpendTheSameMoney(t *testing.T) {
	ctx := context.Background()
	l := newLedger(t)
	payer, payee := account(t, l, "001", 1000), account(t, l, "002", 0)

	var wg sync.WaitGroup
	results := make(chan error, 20)
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- l.Transfer(ctx, payer, payee, 100, fmt.Sprintf("E-%d", i))
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
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 10 || balance(t, l, payer).AvailableCents != 0 || balance(t, l, payee).AvailableCents != 1000 {
		t.Errorf("%d transfers; payer %+v, payee %+v", ok, balance(t, l, payer), balance(t, l, payee))
	}
}
