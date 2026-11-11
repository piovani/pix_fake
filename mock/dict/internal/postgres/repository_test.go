package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/postgres"
	"pixfake/mock/dict/internal/postgres/pgtest"
)

const (
	reqA = "a946d533-7f22-42a5-9a9b-e87cd55c0f4d"
	reqB = "b946d533-7f22-42a5-9a9b-e87cd55c0f4d"
)

func newRepo(t *testing.T) *postgres.Repository {
	t.Helper()
	return postgres.NewRepository(pgtest.NewPool(t))
}

func entry(key string) dict.Entry {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return dict.Entry{
		Key: key, KeyType: dict.KeyEmail,
		Account: dict.Account{Participant: "12345678", Branch: "0001", AccountNumber: "0007654321",
			Type: dict.AccountChecking, OpeningDate: time.Date(2010, 1, 10, 3, 0, 0, 0, time.UTC)},
		Owner:        dict.Owner{Type: dict.LegalPerson, TaxID: "11222333000150", Name: "Padaria Ltda", TradeName: "Padaria"},
		CreationDate: now, KeyOwnershipDate: now,
	}
}

func TestRepository(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	e := entry("pix@bcb.gov.br")

	if err := repo.Create(ctx, e, reqA); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, e.Key)
	if err != nil || got != e {
		t.Fatalf("Get = %+v, %v; want %+v", got, err, e)
	}
	got, err = repo.GetByRequestID(ctx, e.Account.Participant, reqA)
	if err != nil || got != e {
		t.Fatalf("GetByRequestID = %+v, %v", got, err)
	}

	if err := repo.Create(ctx, entry("pix@bcb.gov.br"), reqB); !errors.Is(err, dict.ErrKeyExists) {
		t.Fatalf("duplicate key: err = %v, want ErrKeyExists", err)
	}
	if err := repo.Create(ctx, entry("other@bcb.gov.br"), reqA); !errors.Is(err, dict.ErrRequestIDUsed) {
		t.Fatalf("duplicate request id: err = %v, want ErrRequestIDUsed", err)
	}

	e.Account.AccountNumber, e.Owner.Name = "0001111111", "Padaria Nova Ltda"
	if err := repo.Update(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.Get(ctx, e.Key); got != e {
		t.Fatalf("after Update = %+v, want %+v", got, e)
	}

	existing, err := repo.Existing(ctx, []string{e.Key, "nope@bcb.gov.br"})
	if err != nil || !existing[e.Key] || existing["nope@bcb.gov.br"] {
		t.Fatalf("Existing = %v, %v", existing, err)
	}

	if err := repo.Delete(ctx, e.Key); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, e.Key); !errors.Is(err, dict.ErrNotFound) {
		t.Fatalf("after Delete: err = %v, want ErrNotFound", err)
	}
	if err := repo.Delete(ctx, e.Key); !errors.Is(err, dict.ErrNotFound) {
		t.Fatalf("second Delete: err = %v, want ErrNotFound", err)
	}
	if err := repo.Update(ctx, e); !errors.Is(err, dict.ErrNotFound) {
		t.Fatalf("Update missing: err = %v, want ErrNotFound", err)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t) // already applies the migrations once
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate failed: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("schema_migrations rows = %d, %v; want 1", n, err)
	}
}
