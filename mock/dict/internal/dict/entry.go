// Package dict holds the rules of the simulated Pix key directory (DICT).
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

// Reason is the reason given in write operations (EntryOperationReason).
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
	Participant   string // ISPB of the account provider
	Branch        string
	AccountNumber string
	Type          AccountType
	OpeningDate   time.Time
}

type Owner struct {
	Type      PersonType
	TaxID     string
	Name      string
	TradeName string // legal persons only
}

// Entry is the link between a key, a transactional account and its owner.
type Entry struct {
	Key              string
	KeyType          KeyType
	Account          Account
	Owner            Owner
	CreationDate     time.Time
	KeyOwnershipDate time.Time
}

// KeyStatus is the result of checking a key (/keys/check).
type KeyStatus struct {
	Key      string
	HasEntry bool
}
