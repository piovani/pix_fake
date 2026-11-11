// Package seed defines the test keys that the DICT mock loads with `make seed`.
//
// The ISPBs are fictional. CPFs and CNPJs have valid check digits, so that the
// wallet's own validation (if any) also accepts the data. When changing this
// file, update the "Seed data" table in the README (a test checks the keys).
package seed

import (
	"time"

	"pixfake/mock/dict/internal/dict"
)

// Institutions taking part in the seed.
const (
	WalletISPB = "99000001" // the project's wallet
	AlfaISPB   = "99000002" // Banco Alfa (another bank)
	BetaISPB   = "99000003" // Banco Beta (another bank)
)

var opened = time.Date(2020, 3, 10, 3, 0, 0, 0, time.UTC) // 00:00 in BRT

type holder struct {
	account dict.Account
	owner   dict.Owner
}

func natural(ispb, number string, typ dict.AccountType, cpf, name string) holder {
	return holder{
		account: dict.Account{Participant: ispb, Branch: "0001", AccountNumber: number, Type: typ, OpeningDate: opened},
		owner:   dict.Owner{Type: dict.NaturalPerson, TaxID: cpf, Name: name},
	}
}

func legal(ispb, number string, typ dict.AccountType, cnpj, name, trade string) holder {
	return holder{
		account: dict.Account{Participant: ispb, Branch: "0001", AccountNumber: number, Type: typ, OpeningDate: opened},
		owner:   dict.Owner{Type: dict.LegalPerson, TaxID: cnpj, Name: name, TradeName: trade},
	}
}

func (h holder) key(typ dict.KeyType, key string) dict.Entry {
	return dict.Entry{Key: key, KeyType: typ, Account: h.account, Owner: h.owner}
}

// Entries returns the seed entries.
func Entries() []dict.Entry {
	ana := natural(WalletISPB, "00123456", dict.AccountChecking, "52998224725", "Ana Souza")
	bruno := natural(WalletISPB, "00234567", dict.AccountSavings, "11144477735", "Bruno Lima")
	padaria := legal(WalletISPB, "00345678", dict.AccountChecking, "11222333000181", "Padaria Pão Quente Ltda", "Padaria Pão Quente")
	carlos := natural(AlfaISPB, "00456789", dict.AccountChecking, "39053344705", "Carlos Mendes")
	daniela := natural(BetaISPB, "00567890", dict.AccountTransact, "16899535009", "Daniela Rocha")
	mercadinho := legal(BetaISPB, "00678901", dict.AccountChecking, "45678912000155", "Mercadinho do Zé ME", "Mercadinho do Ze")

	return []dict.Entry{
		// Wallet
		ana.key(dict.KeyCPF, "52998224725"),
		ana.key(dict.KeyPhone, "+5511999990001"),
		ana.key(dict.KeyEmail, "ana.souza@example.com"),
		ana.key(dict.KeyEVP, "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01"),
		bruno.key(dict.KeyCPF, "11144477735"),
		bruno.key(dict.KeyPhone, "+5511999990002"),
		padaria.key(dict.KeyCNPJ, "11222333000181"),
		padaria.key(dict.KeyEmail, "contato@paoquente.example.com"),
		// Banco Alfa
		carlos.key(dict.KeyCPF, "39053344705"),
		carlos.key(dict.KeyPhone, "+5521999990003"),
		carlos.key(dict.KeyEVP, "2b9d4e17-a3c8-4f65-8d02-7e1c6a0b5d93"),
		// Banco Beta
		daniela.key(dict.KeyCPF, "16899535009"),
		daniela.key(dict.KeyEmail, "daniela.rocha@example.com"),
		mercadinho.key(dict.KeyCNPJ, "45678912000155"),
		mercadinho.key(dict.KeyPhone, "+5531999990004"),
	}
}
