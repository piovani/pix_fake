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

// ForcedRejections are test accounts that reject every payment with one reason, one per code of pix.RejectCodes.
// The DICT seed has the same accounts and holders, each with its CPF as a key.
var ForcedRejections = []struct{ Number, Code, TaxID, Name, Type, ISPB string }{
	{"00900004", "DS04", "10293847541", "Helena Moraes", "CACC", AlfaISPB},
	{"00900005", "FRAD", "56473829164", "Igor Batista", "SVGS", BetaISPB},
	{"00900006", "AM02", "91827364564", "Julia Nogueira", "TRAN", AlfaISPB},
	{"00900007", "AG03", "37281946546", "Kleber Duarte", "SVGS", BetaISPB},
	{"00900008", "ED05", "64528193728", "Larissa Prado", "CACC", AlfaISPB},
	{"00900009", "AB09", "82910476340", "Marcos Vieira", "TRAN", BetaISPB},
	{"00900010", "AC14", "45197286300", "Natalia Freitas", "CACC", AlfaISPB},
	{"00900011", "CH11", "73640812913", "Otavio Ramos", "SVGS", BetaISPB},
}

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

	accounts := []pix.Account{
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
	for _, f := range ForcedRejections {
		a := account(f.ISPB, f.Number, f.Type, f.TaxID, f.Name)
		a.Scenario = pix.RejectScenario(f.Code)
		accounts = append(accounts, a)
	}
	return accounts
}
