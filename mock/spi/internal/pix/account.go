package pix

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Account status. The receiving participant rejects payments to accounts that are not active.
const (
	AccountActive  = "ACTIVE"
	AccountClosed  = "CLOSED"
	AccountBlocked = "BLOCKED"
)

// Account scenarios: how the simulated participant behaves when a payment arrives. Besides these two there is
// RejectScenario(code), which rejects every payment with that reason.
const (
	ScenarioNormal   = "NORMAL"
	ScenarioNoAnswer = "NO_ANSWER" // the participant never answers, so the SPI times out
)

// RejectScenario is the scenario in which the participant rejects every payment to the account with the reason code.
func RejectScenario(code string) string { return "REJECT:" + code }

// RejectCode returns the reason code of a rejecting scenario.
func RejectCode(scenario string) (string, bool) { return strings.CutPrefix(scenario, "REJECT:") }

// RejectCodes are the reasons a forced rejection can use, with the detail sent along: the ones a receiving
// participant generates (see the data contract). A wallet can try its handling of each one by paying the
// seeded account that carries it.
var RejectCodes = map[string]string{
	"AB09": "error at the receiving participant",
	"AC14": "wrong type for the receiver account",
	"AG03": "transaction type not supported on the account",
	"AM02": "amount above the limit of the account type",
	"CH11": "CPF/CNPJ of the receiver is incorrect",
	"DS04": "order rejected by the receiving participant",
	"ED05": "processing error at the receiving participant",
	"FRAD": "rejected on suspicion of fraud",
}

// ErrAccountNotFound is returned when the participant has no account with the number.
var ErrAccountNotFound = errors.New("account not found")

// Account is a transactional account of a participant, as the receiving participant knows it.
// The mock keeps it to decide how a simulated participant answers a payment.
type Account struct {
	ISPB        string
	Branch      string
	Number      string // unique within the participant in the mock
	Type        string // CACC, SVGS or TRAN
	HolderTaxID string
	HolderName  string
	Status      string
	Scenario    string
}

// AccountStore keeps the accounts.
type AccountStore interface {
	// CreateAccount stores an account. When the participant already has an account with the
	// number it stores nothing and returns created == false.
	CreateAccount(ctx context.Context, a Account) (created bool, err error)
	// GetAccount returns the account or ErrAccountNotFound.
	GetAccount(ctx context.Context, ispb, number string) (Account, error)
}

// SeedResult lists the accounts created and those that already existed in a seed run.
type SeedResult struct {
	Created  []string // "ispb/number"
	Existing []string
}

// SeedAccounts registers ready-made accounts. It is idempotent: an account that already exists
// is left as it is. All accounts are validated before anything is written.
func SeedAccounts(ctx context.Context, store AccountStore, accounts []Account) (SeedResult, error) {
	seen := map[string]bool{}
	for _, a := range accounts {
		id := a.ISPB + "/" + a.Number
		if seen[id] {
			return SeedResult{}, fmt.Errorf("seed: duplicate account %s", id)
		}
		seen[id] = true
		if err := validateAccount(a); err != nil {
			return SeedResult{}, fmt.Errorf("seed: account %s: %w", id, err)
		}
	}
	var res SeedResult
	for _, a := range accounts {
		id := a.ISPB + "/" + a.Number
		created, err := store.CreateAccount(ctx, a)
		if err != nil {
			return res, fmt.Errorf("seed: create %s: %w", id, err)
		}
		if created {
			res.Created = append(res.Created, id)
		} else {
			res.Existing = append(res.Existing, id)
		}
	}
	return res, nil
}

func validateAccount(a Account) error {
	switch {
	case !ispbPattern.MatchString(a.ISPB):
		return errors.New("invalid ISPB")
	case a.Number == "" || len(a.Number) > 20:
		return errors.New("invalid number")
	case a.Branch != "" && !branchPattern.MatchString(a.Branch):
		return errors.New("invalid branch")
	case !oneOf(a.Type, "CACC", "SVGS", "TRAN"):
		return errors.New("invalid type")
	case !validTaxID(a.HolderTaxID):
		return errors.New("invalid holder CPF or CNPJ")
	case a.HolderName == "":
		return errors.New("missing holder name")
	case !oneOf(a.Status, AccountActive, AccountClosed, AccountBlocked):
		return errors.New("invalid status")
	case !validScenario(a.Scenario):
		return errors.New("invalid scenario")
	}
	return nil
}

func validScenario(s string) bool {
	if oneOf(s, ScenarioNormal, ScenarioNoAnswer) {
		return true
	}
	code, ok := RejectCode(s)
	_, known := RejectCodes[code]
	return ok && known
}
