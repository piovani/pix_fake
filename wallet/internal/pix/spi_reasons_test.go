package pix

import (
	"strings"
	"testing"
)

// Every code the contract lists for pacs.002 (payments and returns) and for camt.025 (reports).
var contractCodes = []string{
	"AB03", "AB09", "AB11", "AC03", "AC06", "AC07", "AC14", "AG03", "AG12", "AM01", "AM02", "AM04", "AM18", "BE01", "CH11", "CH16",
	"DS04", "DS27", "DT02", "ED05", "FF08", "FRAD", "RC09", "RC10", // payments
	"AG13", "AM09", "DT05", // returns
	"AG01", // reports
}

func TestEveryCodeOfTheContractIsMapped(t *testing.T) {
	for _, code := range contractCodes {
		r := ReasonOf(code)
		if r.Cause == CauseUnknown || r.Code != code || r.Message == "" {
			t.Errorf("%s: %+v", code, r)
		}
	}
	if len(reasons) != len(contractCodes) {
		t.Errorf("the table has %d codes and the contract list %d: one of them is out of date", len(reasons), len(contractCodes))
	}
}

func TestReasonCauses(t *testing.T) {
	tests := []struct {
		code     string
		cause    Cause
		tryAgain bool
		alert    bool
	}{
		{"AC03", CauseRecipient, false, false},
		{"AC07", CauseRecipient, false, false},
		{"FRAD", CauseRecipient, false, false},
		{"AB03", CauseTransient, true, false}, // the SPI timeout
		{"AB09", CauseTransient, true, false}, // an error at the receiving participant
		{"ED05", CauseTransient, true, false},
		{"AG13", CauseRule, false, false},
		{"DT05", CauseRule, false, false},
		{"AG12", CauseWallet, false, true}, // a book transfer sent to the SPI is a bug
		{"AM04", CauseWallet, true, true},  // no funds in the PI account: operations top up, then it can work
		{"FF08", CauseWallet, false, true},
		{"RC10", CauseWallet, false, true},
	}
	for _, tt := range tests {
		r := ReasonOf(tt.code)
		if r.Cause != tt.cause || r.TryAgain != tt.tryAgain || r.Alert() != tt.alert {
			t.Errorf("%s: %+v, alert %v; want %s, tryAgain %v, alert %v", tt.code, r, r.Alert(), tt.cause, tt.tryAgain, tt.alert)
		}
	}
}

// Only a transient problem, or a liquidity one that operations fix, is worth trying again.
func TestOnlyTransientAndLiquidityCanBeTriedAgain(t *testing.T) {
	for code, r := range reasons {
		if r.TryAgain && r.Cause != CauseTransient && code != "AM04" {
			t.Errorf("%s (%s) says to try again", code, r.Cause)
		}
		if r.Cause == CauseTransient && !r.TryAgain {
			t.Errorf("%s is transient but says not to try again", code)
		}
	}
}

func TestMessagesAreSafeForTheCustomer(t *testing.T) {
	for code, r := range reasons {
		low := strings.ToLower(r.Message)
		if strings.Contains(low, "fraud") {
			t.Errorf("%s tells the customer about fraud: %q", code, r.Message)
		}
		if strings.Contains(r.Message, code) {
			t.Errorf("%s puts the code in the message: %q", code, r.Message)
		}
		if r.Cause == CauseWallet && (strings.Contains(low, "invalid") || strings.Contains(low, "your")) {
			t.Errorf("%s blames the customer for the wallet's fault: %q", code, r.Message)
		}
	}
	// a payment refused on suspicion of fraud reads like any other refusal
	if ReasonOf("FRAD").Message != ReasonOf("DS04").Message {
		t.Error("FRAD can be told apart from DS04 by its message")
	}
}

func TestUnknownCodeStillClosesThePayment(t *testing.T) {
	r := ReasonOf("ZZ99")
	if r.Cause != CauseUnknown || r.Code != "ZZ99" || r.TryAgain || !r.Alert() || r.Message == "" {
		t.Fatalf("%+v", r)
	}
	if ReasonOf("").Cause != CauseUnknown {
		t.Error("an empty code")
	}
}

func TestRejectionOfAStatus(t *testing.T) {
	rejected := TxStatus{EndToEndID: testE2E, Status: StatusRejected, ReasonCode: "AC07", Detail: "free text from another bank, never shown"}
	r := rejected.Rejection()
	if r.Code != "AC07" || r.Cause != CauseRecipient || strings.Contains(r.Message, "free text") {
		t.Fatalf("%+v", r)
	}
	for _, st := range []string{StatusAccepted, StatusSettled, StatusCredit} {
		if got := (TxStatus{Status: st, ReasonCode: "AC07"}).Rejection(); got != (Rejection{}) {
			t.Errorf("%s is not a rejection: %+v", st, got)
		}
	}
}

func TestRejectionOfAReceipt(t *testing.T) {
	rs, err := ParseCamt025([]byte(camt025FromSPI))
	if err != nil || len(rs) != 2 {
		t.Fatalf("%+v, %v", rs, err)
	}
	if got := rs[0].Rejection(); got != (Rejection{}) {
		t.Errorf("accepted receipt: %+v", got)
	}
	if got := rs[1].Rejection(); got.Code != "FF08" || got.Cause != CauseWallet || !got.Alert() {
		t.Errorf("rejected receipt: %+v", got)
	}
}

func TestRejectionOfANotification(t *testing.T) {
	if r := (Notification{Ref: "res123", Reason: "Invalid XML"}).Rejection(); r.Cause != CauseWallet || !r.Alert() || r.Message == "" {
		t.Fatalf("%+v", r)
	}
}
