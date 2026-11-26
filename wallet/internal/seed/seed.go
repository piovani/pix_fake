// Package seed creates the wallet's test accounts and their Pix keys, the same as in the DICT mock seed. The mocks' code
// is not imported: the values are repeated on purpose, to keep the wallet independent of them.
package seed

import (
	"context"
	"errors"
	"fmt"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
)

type entry struct {
	account ledger.Account
	cents   int64 // initial balance
}

const (
	AnaID     = "0a1b2c3d-0001-4000-8000-000000000001"
	BrunoID   = "0a1b2c3d-0002-4000-8000-000000000002"
	PadariaID = "0a1b2c3d-0003-4000-8000-000000000003"
)

func entries() []entry {
	return []entry{
		{ledger.Account{ID: AnaID, Branch: "0001", Number: "00123456", HolderName: "Ana Souza", TaxID: "52998224725"}, 100000},
		{ledger.Account{ID: BrunoID, Branch: "0001", Number: "00234567", HolderName: "Bruno Lima", TaxID: "11144477735", Type: "SVGS"}, 50000},
		{ledger.Account{ID: PadariaID, Branch: "0001", Number: "00345678", HolderName: "Padaria Pão Quente Ltda", TaxID: "11222333000181"}, 0},
	}
}

// keys use fixed request ids, which is what makes loading them again harmless. They are the ones the DICT mock seed
// creates these keys with, so the CIDs both sides compute from them agree (see pixkey.Service.Reconcile).
func keys() []pixkey.Key {
	return []pixkey.Key{
		{Key: "52998224725", Type: pix.KeyCPF, AccountID: AnaID, RequestID: "462dd5b8-4828-40bf-b224-359a271bd7c9"},
		{Key: "+5511999990001", Type: pix.KeyPhone, AccountID: AnaID, RequestID: "b7d16d8f-f1a5-4106-b507-f735142919d9"},
		{Key: "ana.souza@example.com", Type: pix.KeyEmail, AccountID: AnaID, RequestID: "f432103e-5079-4cdf-877c-982a8efeaecb"},
		{Key: "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01", Type: pix.KeyEVP, AccountID: AnaID, RequestID: "8f47aee1-70d1-4be6-9315-d17c2ac4ebc0"},
		{Key: "11144477735", Type: pix.KeyCPF, AccountID: BrunoID, RequestID: "d3b39643-440e-4ae0-900b-9f09f274c815"},
		{Key: "+5511999990002", Type: pix.KeyPhone, AccountID: BrunoID, RequestID: "75bb4f7a-e4be-42ec-9841-4634c04d049e"},
		{Key: "11222333000181", Type: pix.KeyCNPJ, AccountID: PadariaID, RequestID: "34197520-2f30-484f-b66b-bd80dee58ecb"},
		{Key: "contato@paoquente.example.com", Type: pix.KeyEmail, AccountID: PadariaID, RequestID: "5f49fb05-03c8-4bc1-9e74-0e82d69d367f"},
	}
}

type KeyStore interface {
	Insert(ctx context.Context, k pixkey.Key) (pixkey.Key, error)
}

func Run(ctx context.Context, l *ledger.Ledger, store KeyStore) error {
	for _, e := range entries() {
		if _, err := l.Open(ctx, e.account); err != nil && !errors.Is(err, ledger.ErrAccountExists) {
			return err
		}
		if e.cents > 0 {
			if err := l.Credit(ctx, e.account.ID, e.cents, "seed"); err != nil {
				return err
			}
		}
	}
	for _, k := range keys() {
		if _, err := store.Insert(ctx, k); err != nil {
			return fmt.Errorf("key %s: %w", k.Key, err)
		}
	}
	return nil
}
