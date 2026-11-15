package pix

// Decision is how a simulated receiving participant answers an order.
type Decision struct {
	Accept bool
	// ReasonCode and Detail go with a rejection.
	ReasonCode string
	Detail     string
}

// Responder decides the answer of the simulated participants.
type Responder interface {
	Decide(p Payment) Decision
}

// AcceptAll accepts every order. The rules by creditor account replace it in the rejection step.
type AcceptAll struct{}

func (AcceptAll) Decide(Payment) Decision { return Decision{Accept: true} }
