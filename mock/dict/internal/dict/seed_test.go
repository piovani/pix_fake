package dict_test

import (
	"context"
	"testing"

	"pixfake/mock/dict/internal/dict"
)

const fixedEVP = "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01"

func evpEntry() dict.Entry {
	e := phoneEntry()
	e.Key, e.KeyType = fixedEVP, dict.KeyEVP
	return e
}

func TestSeed(t *testing.T) {
	ctx := context.Background()

	t.Run("creates, and running again does not duplicate", func(t *testing.T) {
		svc := newService()
		entries := []dict.Entry{phoneEntry(), evpEntry()}

		res, err := svc.Seed(ctx, entries)
		if err != nil || len(res.Created) != 2 || len(res.Existing) != 0 {
			t.Fatalf("first run = %+v, %v", res, err)
		}
		res, err = svc.Seed(ctx, entries)
		if err != nil || len(res.Created) != 0 || len(res.Existing) != 2 {
			t.Fatalf("second run = %+v, %v; want 0 created and 2 existing", res, err)
		}

		// The EVP key is the one given, not a generated one.
		got, err := svc.GetEntry(ctx, fixedEVP)
		if err != nil || got.KeyType != dict.KeyEVP || got.CreationDate.IsZero() {
			t.Fatalf("GetEntry(EVP) = %+v, %v", got, err)
		}
	})

	t.Run("does not overwrite an existing entry", func(t *testing.T) {
		svc := newService()
		if _, err := svc.Seed(ctx, []dict.Entry{phoneEntry()}); err != nil {
			t.Fatal(err)
		}
		changed := phoneEntry()
		changed.Owner.Name = "Another Name"
		res, err := svc.Seed(ctx, []dict.Entry{changed})
		if err != nil || len(res.Existing) != 1 {
			t.Fatalf("res = %+v, %v", res, err)
		}
		if got, _ := svc.GetEntry(ctx, changed.Key); got.Owner.Name != "João Silva" {
			t.Fatalf("existing entry was overwritten: %+v", got)
		}
	})

	t.Run("coexists with entries created through the API", func(t *testing.T) {
		svc := newService()
		if _, err := svc.CreateEntry(ctx, psp, createInput(phoneEntry())); err != nil {
			t.Fatal(err)
		}
		res, err := svc.Seed(ctx, []dict.Entry{phoneEntry(), evpEntry()})
		if err != nil || len(res.Created) != 1 || len(res.Existing) != 1 {
			t.Fatalf("res = %+v, %v", res, err)
		}
	})
}

func TestSeedRejectsInvalidData(t *testing.T) {
	ctx := context.Background()

	bad := func(mutate func(e *dict.Entry)) dict.Entry {
		e := phoneEntry()
		mutate(&e)
		return e
	}
	tests := []struct {
		name    string
		entries []dict.Entry
	}{
		{"key with wrong format", []dict.Entry{bad(func(e *dict.Entry) { e.Key = "61988887777" })}},
		{"EVP with wrong format", []dict.Entry{bad(func(e *dict.Entry) { e.Key, e.KeyType = "not-a-uuid", dict.KeyEVP })}},
		{"unknown key type", []dict.Entry{bad(func(e *dict.Entry) { e.KeyType = "X" })}},
		{"SLRY account", []dict.Entry{bad(func(e *dict.Entry) { e.Account.Type = dict.AccountSalary })}},
		{"invalid ISPB", []dict.Entry{bad(func(e *dict.Entry) { e.Account.Participant = "123" })}},
		{"CPF of another owner", []dict.Entry{bad(func(e *dict.Entry) { e.Key, e.KeyType = "99999999999", dict.KeyCPF })}},
		{"duplicate key", []dict.Entry{phoneEntry(), phoneEntry()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newService().Seed(ctx, tt.entries); err == nil {
				t.Fatal("expected an error")
			}
		})
	}

	t.Run("validates everything before writing anything", func(t *testing.T) {
		svc := newService()
		invalid := bad(func(e *dict.Entry) { e.Key, e.Owner.TaxID = "+5561999990000", "123" })
		if _, err := svc.Seed(ctx, []dict.Entry{phoneEntry(), invalid}); err == nil {
			t.Fatal("expected an error")
		}
		if _, err := svc.GetEntry(ctx, phoneEntry().Key); err == nil {
			t.Fatal("the valid entry must not be written when another one is invalid")
		}
	})
}
