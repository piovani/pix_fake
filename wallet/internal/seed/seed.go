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

// Fixed IDs, so the accounts can be used by hand (for example in a gRPC call) after `make up`.
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

// keys use fixed request ids, which is what makes loading them again harmless.
func keys() []pixkey.Key {
	k := func(n int, account string, typ pix.KeyType, key string) pixkey.Key {
		return pixkey.Key{Key: key, Type: typ, AccountID: account, RequestID: fmt.Sprintf("0a1b2c3d-1000-4000-8000-%012d", n)}
	}
	return []pixkey.Key{
		k(1, AnaID, pix.KeyCPF, "52998224725"),
		k(2, AnaID, pix.KeyPhone, "+5511999990001"),
		k(3, AnaID, pix.KeyEmail, "ana.souza@example.com"),
		k(4, AnaID, pix.KeyEVP, "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01"),
		k(5, BrunoID, pix.KeyCPF, "11144477735"),
		k(6, BrunoID, pix.KeyPhone, "+5511999990002"),
		k(7, PadariaID, pix.KeyCNPJ, "11222333000181"),
		k(8, PadariaID, pix.KeyEmail, "contato@paoquente.example.com"),
	}
}

type KeyStore interface {
	Insert(ctx context.Context, k pixkey.Key) (pixkey.Key, error)
}

// Run is idempotent.
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
