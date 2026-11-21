package seed_test

import (
	"context"
	"testing"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/db/dbtest"
	"pixfake/wallet/internal/seed"
)

func TestRunIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	l, keys := ledger.New(pool), pixkey.NewPostgres(pool)

	for range 2 {
		if err := seed.Run(ctx, l, keys); err != nil {
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

	for key, account := range map[string]string{
		"52998224725":                          seed.AnaID,
		"ana.souza@example.com":                seed.AnaID,
		"8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01": seed.AnaID,
		"11144477735":                          seed.BrunoID,
		"+5511999990002":                       seed.BrunoID,
		"contato@paoquente.example.com":        seed.PadariaID,
	} {
		k, found, err := keys.ByKey(ctx, key)
		if err != nil || !found || k.AccountID != account {
			t.Errorf("key %s: %+v, %v, %v", key, k, found, err)
		}
	}
	if all, _ := keys.ByAccount(ctx, seed.AnaID); len(all) != 4 {
		t.Errorf("Ana has %d keys after seeding twice", len(all))
	}
}
