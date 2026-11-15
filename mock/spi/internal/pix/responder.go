package pix

import (
	"context"
	"errors"
)

// Decision is how a simulated receiving participant answers an order.
type Decision struct {
	Accept bool
	// NoAnswer means the participant says nothing: the payment stays pending until the SPI times out.
	NoAnswer bool
	// ReasonCode and Detail go with a rejection.
	ReasonCode string
	Detail     string
}

// Responder decides the answer of the simulated participants.
type Responder interface {
	Decide(ctx context.Context, p Payment) Decision
}

// AcceptAll accepts every order.
type AcceptAll struct{}

func (AcceptAll) Decide(context.Context, Payment) Decision { return Decision{Accept: true} }

// AccountResponder answers according to the receiver's account, so a test picks the outcome by
// picking whom to pay (see "Receiving participant" in the SPI README). In this order:
//
//	account unknown at the participant, or another branch  -> AC03
//	account closed                                         -> AC07
//	account blocked                                        -> AC06
//	scenario "never answers"                               -> no answer (the SPI times out)
//	CPF/CNPJ of the receiver differs from the holder       -> BE01
//	anything else                                          -> accept
type AccountResponder struct {
	Accounts AccountStore
}

func (r AccountResponder) Decide(ctx context.Context, p Payment) Decision {
	acct, err := r.Accounts.GetAccount(ctx, p.PayeeISPB, p.PayeeAccount)
	switch {
	case errors.Is(err, ErrAccountNotFound):
		return decline("AC03", "account does not exist at the participant")
	case err != nil:
		return decline("ED05", "could not read the account")
	case p.PayeeBranch != "" && p.PayeeBranch != acct.Branch:
		return decline("AC03", "branch does not match the account")
	case acct.Status == AccountClosed:
		return decline("AC07", "account is closed")
	case acct.Status == AccountBlocked:
		return decline("AC06", "account is blocked")
	case acct.Scenario == ScenarioNoAnswer:
		return Decision{NoAnswer: true}
	case acct.HolderTaxID != p.PayeeTaxID:
		return decline("BE01", "CPF/CNPJ does not match the account holder")
	}
	return Decision{Accept: true}
}

func decline(code, detail string) Decision { return Decision{ReasonCode: code, Detail: detail} }
