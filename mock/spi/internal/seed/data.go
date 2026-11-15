// Package seed defines the accounts that the SPI mock loads with `make seed`.
//
// They are the same institutions and accounts as the DICT mock seed (mock/dict/internal/seed),
// so a key found in the DICT can be paid in the SPI. A test compares the two files, so they
// cannot drift apart. When changing this file, update the "Seed data" table in the README.
package seed

import "pixfake/mock/spi/internal/pix"

// Institutions taking part in the seed.
const (
	WalletISPB = "99000001" // the project's wallet
	AlfaISPB   = "99000002" // Banco Alfa (simulated by the mock)
	BetaISPB   = "99000003" // Banco Beta (simulated by the mock)
)

// Test accounts of the payment scenarios.
const (
	ClosedAccount  = "00900001" // Banco Alfa: closed
	BlockedAccount = "00900002" // Banco Alfa: blocked
	SilentAccount  = "00900003" // Banco Beta: never answers
)

func account(ispb, number, typ, taxID, name string) pix.Account {
	return pix.Account{
		ISPB: ispb, Branch: "0001", Number: number, Type: typ, HolderTaxID: taxID, HolderName: name,
		Status: pix.AccountActive, Scenario: pix.ScenarioNormal,
	}
}

// Accounts returns the seed accounts.
func Accounts() []pix.Account {
	closed := account(AlfaISPB, ClosedAccount, "CACC", "24681357928", "Eduardo Teixeira")
	closed.Status = pix.AccountClosed
	blocked := account(AlfaISPB, BlockedAccount, "SVGS", "13579246828", "Fernanda Alves")
	blocked.Status = pix.AccountBlocked
	silent := account(BetaISPB, SilentAccount, "TRAN", "86420975310", "Gustavo Pires")
	silent.Scenario = pix.ScenarioNoAnswer

	return []pix.Account{
		// Wallet (a real participant: it decides for itself; the accounts are here for completeness)
		account(WalletISPB, "00123456", "CACC", "52998224725", "Ana Souza"),
		account(WalletISPB, "00234567", "SVGS", "11144477735", "Bruno Lima"),
		account(WalletISPB, "00345678", "CACC", "11222333000181", "Padaria Pão Quente Ltda"),
		// Banco Alfa
		account(AlfaISPB, "00456789", "CACC", "39053344705", "Carlos Mendes"),
		closed,
		blocked,
		// Banco Beta
		account(BetaISPB, "00567890", "TRAN", "16899535009", "Daniela Rocha"),
		account(BetaISPB, "00678901", "CACC", "45678912000155", "Mercadinho do Zé ME"),
		silent,
	}
}
