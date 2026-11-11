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

	t.Run("cria, e rodar de novo não duplica", func(t *testing.T) {
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

		// A chave EVP é a informada, não uma gerada.
		got, err := svc.GetEntry(ctx, fixedEVP)
		if err != nil || got.KeyType != dict.KeyEVP || got.CreationDate.IsZero() {
			t.Fatalf("GetEntry(EVP) = %+v, %v", got, err)
		}
	})

	t.Run("não sobrescreve vínculo existente", func(t *testing.T) {
		svc := newService()
		if _, err := svc.Seed(ctx, []dict.Entry{phoneEntry()}); err != nil {
			t.Fatal(err)
		}
		changed := phoneEntry()
		changed.Owner.Name = "Outro Nome"
		res, err := svc.Seed(ctx, []dict.Entry{changed})
		if err != nil || len(res.Existing) != 1 {
			t.Fatalf("res = %+v, %v", res, err)
		}
		if got, _ := svc.GetEntry(ctx, changed.Key); got.Owner.Name != "João Silva" {
			t.Fatalf("existing entry was overwritten: %+v", got)
		}
	})

	t.Run("convive com vínculos criados pela API", func(t *testing.T) {
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
		{"chave fora do formato", []dict.Entry{bad(func(e *dict.Entry) { e.Key = "61988887777" })}},
		{"EVP fora do formato", []dict.Entry{bad(func(e *dict.Entry) { e.Key, e.KeyType = "nao-e-uuid", dict.KeyEVP })}},
		{"tipo de chave desconhecido", []dict.Entry{bad(func(e *dict.Entry) { e.KeyType = "X" })}},
		{"conta SLRY", []dict.Entry{bad(func(e *dict.Entry) { e.Account.Type = dict.AccountSalary })}},
		{"ISPB inválido", []dict.Entry{bad(func(e *dict.Entry) { e.Account.Participant = "123" })}},
		{"CPF de outro titular", []dict.Entry{bad(func(e *dict.Entry) { e.Key, e.KeyType = "99999999999", dict.KeyCPF })}},
		{"chave repetida", []dict.Entry{phoneEntry(), phoneEntry()}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := newService().Seed(ctx, tt.entries); err == nil {
				t.Fatal("expected an error")
			}
		})
	}

	t.Run("valida tudo antes de gravar qualquer coisa", func(t *testing.T) {
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
