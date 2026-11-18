package seed_test

import (
	"context"
	"testing"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/platform/db/dbtest"
	"pixfake/wallet/internal/seed"
)

func TestRunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	l := ledger.New(dbtest.NewPool(t))

	for range 2 {
		if err := seed.Run(ctx, l); err != nil {
			t.Fatal(err)
		}
	}
	b, err := l.Balance(ctx, seed.AnaID)
	if err != nil || b.AvailableCents != 100000 {
		t.Fatalf("Ana after seeding twice: %+v, %v", b, err)
	}
	if b, _ := l.Balance(ctx, seed.PadariaID); b.AvailableCents != 0 {
		t.Fatalf("Padaria: %+v", b)
	}
}
