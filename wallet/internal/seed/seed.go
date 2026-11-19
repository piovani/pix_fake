// Package seed creates the wallet's test accounts. They are the same people as the wallet's accounts in the DICT mock
// seed (same branch and account number), so a key found in the DICT can be recognized as one of our own accounts.
// The mocks' code is not imported: the numbers are repeated here on purpose, to keep the wallet independent of them.
package seed

import (
	"context"
	"errors"

	"pixfake/wallet/internal/ledger"
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

// Run loads the accounts and their initial balances. It is idempotent: running it again changes nothing.
func Run(ctx context.Context, l *ledger.Ledger) error {
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
	return nil
}
