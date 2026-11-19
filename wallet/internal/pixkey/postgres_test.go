package pixkey_test

import (
	"context"
	"errors"
	"testing"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/db/dbtest"
)

const (
	req1 = "11111111-1111-4111-8111-111111111111"
	req2 = "22222222-2222-4222-8222-222222222222"
)

func TestPostgresStore(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	l := ledger.New(pool)
	store := pixkey.NewPostgres(pool)

	acct, err := l.Open(ctx, ledger.Account{Branch: "0001", Number: "1", HolderName: "Ana", TaxID: "52998224725"})
	if err != nil {
		t.Fatal(err)
	}

	if _, found, err := store.ByKey(ctx, "a@example.com"); err != nil || found {
		t.Fatalf("empty store: found = %v, err = %v", found, err)
	}
	stored, err := store.Insert(ctx, pixkey.Key{Key: "a@example.com", Type: pix.KeyEmail, AccountID: acct.ID, RequestID: req1})
	if err != nil || stored.CreatedAt.IsZero() || stored.Type != pix.KeyEmail {
		t.Fatalf("insert: %+v, %v", stored, err)
	}

	byReq, found, err := store.ByRequestID(ctx, req1)
	if err != nil || !found || byReq.Key != "a@example.com" || byReq.AccountID != acct.ID {
		t.Fatalf("by request id: %+v, %v, %v", byReq, found, err)
	}
	if _, found, _ := store.ByKey(ctx, "a@example.com"); !found {
		t.Fatal("by key: not found")
	}

	again, err := store.Insert(ctx, pixkey.Key{Key: "a@example.com", Type: pix.KeyEmail, AccountID: acct.ID, RequestID: req1})
	if err != nil || again.Key != stored.Key {
		t.Fatalf("same request id twice: %+v, %v", again, err)
	}
	if _, err := store.Insert(ctx, pixkey.Key{Key: "a@example.com", Type: pix.KeyEmail, AccountID: acct.ID, RequestID: req2}); !errors.Is(err, pixkey.ErrKeyTaken) {
		t.Fatalf("same key, other request: %v", err)
	}
	if _, err := store.Insert(ctx, pixkey.Key{Key: "x", Type: pix.KeyEmail, AccountID: "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01", RequestID: req2}); err == nil {
		t.Fatal("a key for an account that does not exist was stored")
	}

	if _, err := store.Insert(ctx, pixkey.Key{Key: "b@example.com", Type: pix.KeyEmail, AccountID: acct.ID, RequestID: req2}); err != nil {
		t.Fatal(err)
	}
	keys, err := store.ByAccount(ctx, acct.ID)
	if err != nil || len(keys) != 2 || keys[0].Key != "a@example.com" || keys[1].Key != "b@example.com" {
		t.Fatalf("by account: %+v, %v", keys, err)
	}

	if err := store.Delete(ctx, "a@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(ctx, "a@example.com"); err != nil {
		t.Fatalf("deleting twice: %v", err)
	}
	if _, found, _ := store.ByKey(ctx, "a@example.com"); found {
		t.Fatal("the key is still there")
	}
	if keys, _ := store.ByAccount(ctx, acct.ID); len(keys) != 1 {
		t.Fatalf("after delete: %+v", keys)
	}
}
