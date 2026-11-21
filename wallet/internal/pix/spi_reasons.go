package pix

// What the wallet does with a rejection that comes from the SPI. A rejection is final: the payment, return or report
// did not happen, so the balance reserved for it goes back. What differs is what to tell the customer, whether the same
// payment can work later, and whether someone has to look at it.

// Cause tells who the rejection is about.
type Cause string

const (
	// CauseRecipient: the receiving side cannot take this payment (account closed, wrong data...). The customer can
	// check the data or choose another account or key; trying the same payment again changes nothing.
	CauseRecipient Cause = "recipient"
	// CauseRule: the Pix rules do not allow it (a return of a return, past the deadline, above the original amount).
	CauseRule Cause = "rule"
	// CauseTransient: a timeout or a processing error on the way. The same payment may work if made again later.
	CauseTransient Cause = "transient"
	// CauseWallet: the wallet's own fault or limit (a malformed message, a payment that should have been a book
	// transfer, no funds in the institution's PI account). The customer did nothing wrong; operations must look.
	CauseWallet Cause = "wallet"
	// CauseUnknown: a code this table does not know. Treated as final, and operations must look.
	CauseUnknown Cause = "unknown"
)

// Rejection is the wallet's reading of a reason code of the SPI.
type Rejection struct {
	Code  string
	Cause Cause
	// TryAgain is true when the same payment, made again later, may work. Make it as a new payment (a new EndToEndId):
	// the SPI answers a repeated one with the outcome it already had.
	TryAgain bool
	// Message is safe to show to the customer. It never repeats the free text another institution sent, and it does not
	// say that a payment was refused on suspicion of fraud.
	Message string
}

// Alert reports whether operations should look at it: the wallet's own fault, or a code nobody mapped.
func (r Rejection) Alert() bool { return r.Cause == CauseWallet || r.Cause == CauseUnknown }

const (
	msgNotProcessed = "The payment could not be processed. It was not made."
	msgRefused      = "The receiving institution refused the payment."
)

// reasons covers the codes of pacs.002 (payments and returns) and of camt.025 (reports) that the wallet can receive.
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

// ReasonOf reads a reason code. A code it does not know comes back as CauseUnknown, never as an error: the SPI may
// add codes, and a payment must still be closed.
func ReasonOf(code string) Rejection {
	if r, ok := reasons[code]; ok {
		r.Code = code
		return r
	}
	return Rejection{Code: code, Cause: CauseUnknown, Message: "The payment was refused. It was not made."}
}

// Rejection reads the reason of a rejected status. For a status that is not a rejection it returns the zero value.
func (s TxStatus) Rejection() Rejection {
	if !s.Rejected() {
		return Rejection{}
	}
	return ReasonOf(s.ReasonCode)
}

// Rejection reads the reason of a rejected receipt. A rejected report can be fixed and sent again; nothing was
// recorded for it. For an accepted receipt it returns the zero value.
func (r Receipt) Rejection() Rejection {
	if !r.Rejected() {
		return Rejection{}
	}
	return ReasonOf(r.ReasonCode)
}

// Rejection reads an admi.002: the SPI could not process a message and nothing was done with it. The wallet builds
// its messages itself, so this is always its own fault. The message to find is the one with the PI-ResourceId in Ref.
func (n Notification) Rejection() Rejection {
	return Rejection{Code: "admi.002", Cause: CauseWallet, Message: msgNotProcessed}
}
