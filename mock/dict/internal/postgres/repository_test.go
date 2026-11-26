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

// stamp gives the entry what the service sets before writing it: the RequestId and the CID.
func stamp(t *testing.T, e dict.Entry, requestID string) dict.Entry {
	t.Helper()
	cid, err := dict.CID(e, requestID)
	if err != nil {
		t.Fatal(err)
	}
	e.RequestID, e.CID = requestID, cid
	return e
}

func TestRepository(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	e := stamp(t, entry("pix@bcb.gov.br"), reqA)

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

	if err := repo.Create(ctx, stamp(t, entry("pix@bcb.gov.br"), reqB), reqB); !errors.Is(err, dict.ErrKeyExists) {
		t.Fatalf("duplicate key: err = %v, want ErrKeyExists", err)
	}
	if err := repo.Create(ctx, stamp(t, entry("other@bcb.gov.br"), reqA), reqA); !errors.Is(err, dict.ErrRequestIDUsed) {
		t.Fatalf("duplicate request id: err = %v, want ErrRequestIDUsed", err)
	}

	created := e
	e.Account.AccountNumber, e.Owner.Name = "0001111111", "Padaria Nova Ltda"
	e = stamp(t, e, reqA)
	if err := repo.Update(ctx, e); err != nil {
		t.Fatal(err)
	}
	if got, _ = repo.Get(ctx, e.Key); got != e {
		t.Fatalf("after Update = %+v, want %+v", got, e)
	}

	if got, err := repo.GetByCID(ctx, e.CID); err != nil || got.Key != e.Key {
		t.Fatalf("GetByCID = %+v, %v", got, err)
	}
	if _, err := repo.GetByCID(ctx, created.CID); !errors.Is(err, dict.ErrNotFound) {
		t.Fatalf("the CID from before the update: %v", err)
	}
	if cids, err := repo.CIDs(ctx, e.Account.Participant, e.KeyType); err != nil || len(cids) != 1 || cids[0] != e.CID {
		t.Fatalf("CIDs = %v, %v", cids, err)
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

	// create, update (removed + added), delete; failed writes leave no event
	events, err := repo.CIDEvents(ctx, e.Account.Participant, e.KeyType)
	if err != nil || len(events) != 4 {
		t.Fatalf("events = %+v, %v", events, err)
	}
	want := []struct {
		typ dict.CIDEventType
		cid string
	}{{dict.CIDAdded, created.CID}, {dict.CIDRemoved, created.CID}, {dict.CIDAdded, e.CID}, {dict.CIDRemoved, e.CID}}
	for i, w := range want {
		if events[i].Type != w.typ || events[i].CID != w.cid || events[i].Timestamp.IsZero() {
			t.Fatalf("event %d = %+v, want %s %s", i, events[i], w.typ, w.cid)
		}
	}
	if cids, _ := repo.CIDs(ctx, e.Account.Participant, e.KeyType); len(cids) != 0 {
		t.Fatalf("CIDs after delete = %v", cids)
	}
}

func TestRepositoryReconciliationRecords(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	v, err := repo.CreateSyncVerification(ctx, dict.SyncVerification{Participant: "12345678", KeyType: dict.KeyCPF,
		Verifier: dict.ZeroVSync, DictVSync: dict.ZeroVSync, Result: dict.SyncOK})
	if err != nil || v.ID == 0 {
		t.Fatalf("sync verification = %+v, %v", v, err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	f, err := repo.CreateCIDFile(ctx, dict.CIDFile{Status: dict.FileAvailable, Participant: "12345678", KeyType: dict.KeyCPF,
		RequestTime: now, CreationTime: now, Content: []byte("abc\n"), Sha256: "x"})
	if err != nil || f.ID == 0 {
		t.Fatalf("file = %+v, %v", f, err)
	}
	got, err := repo.GetCIDFile(ctx, f.ID)
	if err != nil || string(got.Content) != "abc\n" || got.Status != dict.FileAvailable || !got.CreationTime.Equal(now) || got.KeyType != dict.KeyCPF {
		t.Fatalf("get file = %+v, %v", got, err)
	}
	if _, err := repo.GetCIDFile(ctx, f.ID+1); !errors.Is(err, dict.ErrNotFound) {
		t.Fatalf("unknown file: %v", err)
	}
}

// Entries written before the reconciliation existed get their CID, and its ADDED event, when the mock starts.
func TestMigrateBackfillsCIDs(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t)
	e := entry("old@bcb.gov.br")
	if _, err := pool.Exec(ctx, `
		INSERT INTO entries (key, key_type, participant, branch, account_number, account_type, opening_date,
			owner_type, owner_tax_id, owner_name, owner_trade_name, request_id, creation_date, key_ownership_date)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		e.Key, e.KeyType, e.Account.Participant, e.Account.Branch, e.Account.AccountNumber, e.Account.Type,
		e.Account.OpeningDate, e.Owner.Type, e.Owner.TaxID, e.Owner.Name, e.Owner.TradeName, reqA, e.CreationDate, e.KeyOwnershipDate); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewRepository(pool)
	got, err := repo.Get(ctx, e.Key)
	if want := stamp(t, e, reqA); err != nil || got.CID != want.CID {
		t.Fatalf("cid = %q, want %q (%v)", got.CID, want.CID, err)
	}
	events, err := repo.CIDEvents(ctx, e.Account.Participant, e.KeyType)
	if err != nil || len(events) != 1 || events[0].Type != dict.CIDAdded || !events[0].Timestamp.Equal(e.CreationDate) {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if events, _ := repo.CIDEvents(ctx, e.Account.Participant, e.KeyType); len(events) != 1 {
		t.Fatalf("a second start added events: %+v", events)
	}
}

func TestMigrateTwice(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.NewPool(t) // already applies the migrations once
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("second Migrate failed: %v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&n); err != nil || n != 3 {
		t.Fatalf("schema_migrations rows = %d, %v; want 3", n, err)
	}
}

// DeleteAll and ListEntries serve the control API.
func TestRepositoryControl(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)

	other := entry("b@bcb.gov.br")
	other.Account.Participant = "87654321"
	for i, e := range []dict.Entry{entry("c@bcb.gov.br"), entry("a@bcb.gov.br"), other} {
		req := []string{reqA, reqB, "c946d533-7f22-42a5-9a9b-e87cd55c0f4d"}[i]
		if err := repo.Create(ctx, stamp(t, e, req), req); err != nil {
			t.Fatal(err)
		}
	}

	all, err := repo.ListEntries(ctx, "", 0)
	if err != nil || len(all) != 3 || all[0].Key != "a@bcb.gov.br" || all[1].Key != "b@bcb.gov.br" || all[2].Key != "c@bcb.gov.br" {
		t.Fatalf("all = %v, %v; want the 3 entries ordered by key", keys(all), err)
	}
	mine, _ := repo.ListEntries(ctx, "12345678", 0)
	if len(mine) != 2 {
		t.Fatalf("participant filter = %v", keys(mine))
	}
	if two, _ := repo.ListEntries(ctx, "", 2); len(two) != 2 {
		t.Fatalf("limit = %v", keys(two))
	}

	if err := repo.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if left, _ := repo.ListEntries(ctx, "", 0); len(left) != 0 {
		t.Fatalf("entries left: %v", keys(left))
	}
	// The RequestIds are free again.
	if err := repo.Create(ctx, stamp(t, entry("a@bcb.gov.br"), reqA), reqA); err != nil {
		t.Fatalf("create after DeleteAll: %v", err)
	}
	if events, _ := repo.CIDEvents(ctx, "12345678", dict.KeyEmail); len(events) != 1 {
		t.Fatalf("DeleteAll must clear the CID events too: %+v", events)
	}
}

func keys(es []dict.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Key
	}
	return out
}
