// Package dict contém as regras do diretório de chaves Pix (DICT) simulado.
package dict

import "time"

type KeyType string

const (
	KeyCPF   KeyType = "CPF"
	KeyCNPJ  KeyType = "CNPJ"
	KeyPhone KeyType = "PHONE"
	KeyEmail KeyType = "EMAIL"
	KeyEVP   KeyType = "EVP"
)

type AccountType string

const (
	AccountChecking AccountType = "CACC"
	AccountTransact AccountType = "TRAN"
	AccountSalary   AccountType = "SLRY"
	AccountSavings  AccountType = "SVGS"
	AccountOther    AccountType = "OTHR"
)

type PersonType string

const (
	NaturalPerson PersonType = "NATURAL_PERSON"
	LegalPerson   PersonType = "LEGAL_PERSON"
)

// Reason é o motivo informado nas operações de escrita (EntryOperationReason).
type Reason string

const (
	ReasonUserRequested  Reason = "USER_REQUESTED"
	ReasonAccountClosure Reason = "ACCOUNT_CLOSURE"
	ReasonBranchTransfer Reason = "BRANCH_TRANSFER"
	ReasonReconciliation Reason = "RECONCILIATION"
	ReasonFraud          Reason = "FRAUD"
	ReasonRFBValidation  Reason = "RFB_VALIDATION"
)

type Account struct {
	Participant   string // ISPB do provedor da conta
	Branch        string
	AccountNumber string
	Type          AccountType
	OpeningDate   time.Time
}

type Owner struct {
	Type      PersonType
	TaxID     string
	Name      string
	TradeName string // apenas pessoa jurídica
}

// Entry é o vínculo entre chave, conta transacional e titular.
type Entry struct {
	Key              string
	KeyType          KeyType
	Account          Account
	Owner            Owner
	CreationDate     time.Time
	KeyOwnershipDate time.Time
}

// KeyStatus é o resultado da verificação de uma chave (/keys/check).
type KeyStatus struct {
	Key      string
	HasEntry bool
}
