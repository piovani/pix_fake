package pix

// A rejection is final and the reserved money goes back; what differs is the message, whether trying again can work, and who must look.

type Cause string

const (
	// CauseRecipient: the receiving side cannot take it; the same payment will fail again.
	CauseRecipient Cause = "recipient"
	// CauseRule: the Pix rules forbid it (return of a return, past the deadline, above the original).
	CauseRule Cause = "rule"
	// CauseTransient: a timeout or processing error; the same payment may work later.
	CauseTransient Cause = "transient"
	// CauseWallet: the wallet's own fault or limit; the customer did nothing wrong and operations must look.
	CauseWallet Cause = "wallet"
	// CauseUnknown: a code this table does not know. Treated as final, and operations must look.
	CauseUnknown Cause = "unknown"
)

type Rejection struct {
	Code  string
	Cause Cause
	// TryAgain: make it as a new payment (a new EndToEndId), since the SPI answers a repeated one with the outcome it had.
	TryAgain bool
	// Message is safe to show: it never repeats another bank's text or hints at fraud.
	Message string
}

// Alert: the wallet's own fault, or a code nobody mapped.
func (r Rejection) Alert() bool { return r.Cause == CauseWallet || r.Cause == CauseUnknown }

const (
	msgNotProcessed = "The payment could not be processed. It was not made."
	msgRefused      = "The receiving institution refused the payment."
)

var reasons = map[string]Rejection{
	// the receiving participant refused it
	"AC03": {Cause: CauseRecipient, Message: "The receiving account does not exist or its data is wrong."},
	"AC06": {Cause: CauseRecipient, Message: "The receiving account is blocked."},
	"AC07": {Cause: CauseRecipient, Message: "The receiving account is closed."},
	"AC14": {Cause: CauseRecipient, Message: "The receiving account cannot take this payment: its type does not match."},
	"AG03": {Cause: CauseRecipient, Message: "The receiving account cannot receive this kind of payment."},
	"AM02": {Cause: CauseRecipient, Message: "The amount is above the limit of the receiving account."},
	"BE01": {Cause: CauseRecipient, Message: "The receiver's tax ID does not match the account holder."},
	"CH11": {Cause: CauseRecipient, Message: "The receiver's tax ID does not match the account holder."},
	"DS04": {Cause: CauseRecipient, Message: msgRefused},
	"FRAD": {Cause: CauseRecipient, Message: msgRefused}, // the customer is not told why

	// timeouts and processing errors on the way
	"AB03": {Cause: CauseTransient, TryAgain: true, Message: "The payment took too long to settle. It was not made; try again."},
	"AB09": {Cause: CauseTransient, TryAgain: true, Message: "The receiving institution had an error. The payment was not made; try again later."},
	"AB11": {Cause: CauseTransient, TryAgain: true, Message: "The payment could not be sent in time. It was not made; try again."},
	"ED05": {Cause: CauseTransient, TryAgain: true, Message: "A processing error stopped the payment. It was not made; try again later."},
	"DS27": {Cause: CauseTransient, TryAgain: true, Message: "The receiving institution is not available in Pix right now. Try again later."},

	// the Pix rules, for returns
	"AG13": {Cause: CauseRule, Message: "A return cannot be returned again."},
	"DT05": {Cause: CauseRule, Message: "The deadline to return this payment has passed."},
	"AM09": {Cause: CauseRule, Message: "The amount is above what is left of the original payment."},

	// the wallet's own fault or limit
	"AG12": {Cause: CauseWallet, Message: msgNotProcessed},                                       // a payment inside the institution must not go to the SPI
	"AG01": {Cause: CauseWallet, Message: msgNotProcessed},                                       // a report of a transfer that had to go through the SPI
	"AM04": {Cause: CauseWallet, TryAgain: true, Message: msgNotProcessed + " Try again later."}, // no funds in the PI account
	"AM01": {Cause: CauseWallet, Message: msgNotProcessed},
	"AM18": {Cause: CauseWallet, Message: msgNotProcessed},
	"CH16": {Cause: CauseWallet, Message: msgNotProcessed},
	"DT02": {Cause: CauseWallet, Message: msgNotProcessed},
	"FF08": {Cause: CauseWallet, Message: msgNotProcessed},
	"RC09": {Cause: CauseWallet, Message: msgNotProcessed},
	"RC10": {Cause: CauseWallet, Message: msgNotProcessed}, // the ISPB came from the DICT
}

// A code it does not know is CauseUnknown, never an error: the SPI may add codes and a payment must still close.
func ReasonOf(code string) Rejection {
	if r, ok := reasons[code]; ok {
		r.Code = code
		return r
	}
	return Rejection{Code: code, Cause: CauseUnknown, Message: "The payment was refused. It was not made."}
}

func (s TxStatus) Rejection() Rejection {
	if !s.Rejected() {
		return Rejection{}
	}
	return ReasonOf(s.ReasonCode)
}

// A rejected report can be fixed and sent again.
func (r Receipt) Rejection() Rejection {
	if !r.Rejected() {
		return Rejection{}
	}
	return ReasonOf(r.ReasonCode)
}

// The wallet builds its messages itself, so this is always its own fault; Ref is the PI-ResourceId.
func (n Notification) Rejection() Rejection {
	return Rejection{Code: "admi.002", Cause: CauseWallet, Message: msgNotProcessed}
}
