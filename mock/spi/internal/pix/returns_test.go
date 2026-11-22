package pix_test

import (
	"context"
	"testing"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/seed"
)

const (
	rtr1 = "D99000002202611141200RETURN00001"
	rtr2 = "D99000002202611141200RETURN00002"
	rtr3 = "D99000002202611141200RETURN00003"
)

// returnReq builds the pacs.004 a participant sends. The default is Banco Alfa returning the payment e2eA to the wallet.
type returnReq struct {
	id           string
	e2e          string
	sender       string
	remetente    string // participant that is debited, defaults to the sender
	destinatario string // participant that receives the money, defaults to the wallet
	amount       string
	mutate       func(*iso.Return)
	mutateMsg    func(*iso.Pacs004)
}

func (r returnReq) body(t *testing.T) []byte {
	t.Helper()
	if r.id == "" {
		r.id = rtr1
	}
	if r.e2e == "" {
		r.e2e = e2eA
	}
	if r.sender == "" {
		r.sender = alfa
	}
	if r.remetente == "" {
		r.remetente = r.sender
	}
	if r.destinatario == "" {
		r.destinatario = wallet
	}
	if r.amount == "" {
		r.amount = "10.50"
	}
	ret := iso.NewReturn(r.id, r.e2e, r.amount, "MD06", r.remetente, r.destinatario)
	if r.mutate != nil {
		r.mutate(&ret)
	}
	msg := iso.NewPacs004(r.sender, pix.SPIISPB, "M"+r.sender+"abcdefghijklmnopqrstuvw", iso.FormatTime(now), ret)
	if r.mutateMsg != nil {
		r.mutateMsg(msg)
	}
	body, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (e *env) sendReturn(r returnReq) {
	e.t.Helper()
	if r.sender == "" {
		r.sender = alfa
	}
	e.send(r.sender, r.body(e.t))
}

// returnAnswer is the pacs.002 the participant that receives a return sends to the SPI.
func returnAnswer(t *testing.T, from, rtr, e2e, status, reason string) []byte {
	t.Helper()
	st := iso.NewTxStatus(e2e, status).ForReturn(rtr)
	if reason != "" {
		st = st.WithReason(reason, "")
	}
	body, err := iso.NewPacs002(from, pix.SPIISPB, "M"+from+"abcdefghijklmnopqrstuvw", iso.FormatTime(now), st).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// paidByWallet starts with a settled payment e2eA of 10.50 from the wallet to Carlos Mendes, at Banco Alfa.
func paidByWallet(t *testing.T) *env {
	t.Helper()
	e := newRulesEnv(t)
	e.sendOrder(order{})
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("setup: payment is %s", p.Status)
	}
	e.read(wallet)
	e.read(alfa)
	return e
}

// carlosPays makes the payer Carlos Mendes, whose account exists at Banco Alfa.
func carlosPays(tx *iso.CreditTransfer) {
	tx.Debtor.ID.PrvtID.Othr.ID = "39053344705"
	tx.DebtorAcct.ID.Othr.Number = "00456789"
}

// paidToWallet starts with a settled payment e2eA of 10.50 from Carlos Mendes (Banco Alfa) to the wallet.
func paidToWallet(t *testing.T) *env {
	t.Helper()
	e := newRulesEnv(t)
	e.sendOrder(order{sender: alfa, payee: wallet, mutateTx: carlosPays})
	e.read(wallet)
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("setup: payment is %s", p.Status)
	}
	e.read(wallet)
	e.read(alfa)
	return e
}

// --- Happy paths ---

func TestReturnWaitsForReceiver(t *testing.T) {
	e := paidByWallet(t)
	e.sendReturn(returnReq{})

	r := e.payment(rtr1)
	if r.Kind != pix.KindReturn || r.Status != pix.StatusPending || r.EndToEndID != e2eA || r.AmountCents != 1050 || r.ReturnReason != "MD06" ||
		r.PayerISPB != alfa || r.PayeeISPB != wallet || r.PayeeAccount != "00123456" || r.PayerAccount != "00456789" {
		t.Fatalf("return = %+v", r)
	}
	if got := e.readStatuses(alfa); len(got) != 0 {
		t.Fatalf("sender got %+v before the wallet answered", got)
	}

	in := e.read(wallet)
	if len(in) != 1 {
		t.Fatalf("wallet received %d messages, want the forwarded pacs.004", len(in))
	}
	fwd, err := iso.ParsePacs004(in[0].Body)
	if err != nil || fwd.AppHdr.Fr.ISPB() != pix.SPIISPB || fwd.AppHdr.To.ISPB() != wallet || fwd.AppHdr.MsgDefIdr != iso.Pacs004ID {
		t.Fatalf("forwarded message: %v, header %+v", err, fwd.AppHdr)
	}
	if ret := fwd.Returns()[0]; ret.RtrID != rtr1 || ret.OrgnlEndToEnd != e2eA || ret.Amount.Value != "10.50" || ret.Reason() != "MD06" {
		t.Fatalf("forwarded return = %+v", ret)
	}

	e.send(wallet, returnAnswer(t, wallet, rtr1, e2eA, iso.StatusAcceptedByReceiver, ""))
	if r = e.payment(rtr1); r.Status != pix.StatusSettled {
		t.Fatalf("return status = %s, want SETTLED", r.Status)
	}
	sts := e.readStatuses(alfa)
	if len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer || sts[0].OrgnlInstrID != rtr1 || sts[0].OrgnlEndToEnd != e2eA {
		t.Fatalf("sender statuses = %+v; want ACSC for the return %s of payment %s", sts, rtr1, e2eA)
	}
	if got := e.readStatuses(wallet); len(got) != 1 || got[0].Status != iso.StatusSettledReceiver || got[0].OrgnlInstrID != rtr1 {
		t.Fatalf("wallet statuses = %+v; want ACCC", got)
	}
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("the original payment changed to %s", p.Status)
	}
}

func TestReturnToBank(t *testing.T) {
	e := paidToWallet(t)
	// The wallet returns what Carlos Mendes paid; Banco Alfa is simulated.
	e.sendReturn(returnReq{sender: wallet, destinatario: alfa})

	if r := e.payment(rtr1); r.Status != pix.StatusSettled || r.PayerISPB != wallet || r.PayeeISPB != alfa || r.PayeeAccount != "00456789" {
		t.Fatalf("return = %+v", r)
	}
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer || sts[0].OrgnlInstrID != rtr1 {
		t.Fatalf("wallet statuses = %+v; want ACSC", sts)
	}
}

func TestReturnPartial(t *testing.T) {
	e := paidToWallet(t)
	e.sendReturn(returnReq{id: rtr1, sender: wallet, destinatario: alfa, amount: "5.00"})
	e.sendReturn(returnReq{id: rtr2, sender: wallet, destinatario: alfa, amount: "5.00"})
	for _, id := range []string{rtr1, rtr2} {
		if r := e.payment(id); r.Status != pix.StatusSettled {
			t.Fatalf("%s = %s", id, r.Status)
		}
	}

	// 5.00 + 5.00 + 1.00 is more than the 10.50 that was paid.
	e.sendReturn(returnReq{id: rtr3, sender: wallet, destinatario: alfa, amount: "1.00"})
	r := e.payment(rtr3)
	if r.Status != pix.StatusRejected || r.ReasonCode != "AM09" {
		t.Fatalf("third return = %+v; want rejected with AM09", r)
	}
	// What is left (0.50) can still be returned: a rejected return does not count.
	e.sendReturn(returnReq{id: "D99000002202611141200RETURN00004", sender: wallet, destinatario: alfa, amount: "0.50"})
	if r := e.payment("D99000002202611141200RETURN00004"); r.Status != pix.StatusSettled {
		t.Fatalf("last return = %+v; want settled", r)
	}
}

// --- Rejection by the participant that receives the return ---

func TestReturnRejectedByReceiver(t *testing.T) {
	e := paidByWallet(t)
	e.sendReturn(returnReq{})
	e.read(wallet)
	e.send(wallet, returnAnswer(t, wallet, rtr1, e2eA, iso.StatusRejected, "AC07"))

	r := e.payment(rtr1)
	if r.Status != pix.StatusRejected || r.ReasonCode != "AC07" {
		t.Fatalf("return = %+v", r)
	}
	sts := e.readStatuses(alfa)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AC07" || sts[0].OrgnlInstrID != rtr1 || sts[0].OrgnlEndToEnd != e2eA {
		t.Fatalf("sender statuses = %+v", sts)
	}
}

func TestReturnClosedAccount(t *testing.T) {
	e := newRulesEnv(t)
	// Eduardo Teixeira paid the wallet from an account that has since been closed.
	e.sendOrder(order{sender: alfa, payee: wallet, mutateTx: func(tx *iso.CreditTransfer) {
		tx.Debtor.ID.PrvtID.Othr.ID = "24681357928"
		tx.DebtorAcct.ID.Othr.Number = seed.ClosedAccount
	}})
	e.read(wallet)
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))
	e.read(wallet)

	e.sendReturn(returnReq{sender: wallet, destinatario: alfa})
	if r := e.payment(rtr1); r.Status != pix.StatusRejected || r.ReasonCode != "AC07" {
		t.Fatalf("return = %+v; want rejected with AC07", r)
	}
}

func TestReturnTimeout(t *testing.T) {
	e := paidByWallet(t)
	e.sendReturn(returnReq{})
	e.read(wallet)

	e.advance(pix.DefaultTimeout + time.Second)
	if n := e.proc.ExpirePending(context.Background()); n != 1 {
		t.Fatalf("expired %d, want the pending return", n)
	}
	sts := e.readStatuses(alfa)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AB03" || sts[0].OrgnlInstrID != rtr1 {
		t.Fatalf("sender statuses = %+v; want RJCT AB03", sts)
	}
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("original payment = %s; a timed out return must not touch it", p.Status)
	}
}

// --- Rejection by the SPI ---

func TestReturnRejected(t *testing.T) {
	tests := []struct {
		name string
		req  returnReq
		want string
	}{
		{"zero amount", returnReq{amount: "0.00"}, "AM01"},
		{"invalid amount", returnReq{amount: "1.234"}, "CH16"},
		{"normal priority", returnReq{mutate: func(r *iso.Return) { r.Priority = "NORM" }}, "CH16"},
		{"reason BE08 (MED)", returnReq{mutate: func(r *iso.Return) { r.RtrRsnInf.Rsn.Cd = "BE08" }}, "CH16"},
		{"reason SL02 (Pix Saque)", returnReq{mutate: func(r *iso.Return) { r.RtrRsnInf.Rsn.Cd = "SL02" }}, "CH16"},
		{"charge bearer", returnReq{mutate: func(r *iso.Return) { r.ChrgBr = "DEBT" }}, "CH16"},
		{"RtrId 13 hours old", returnReq{id: "D99000002202611132300CCCCCCCCCCC"}, "FF08"},
		{"debited participant is not the sender", returnReq{remetente: beta}, "AGNT"},
		{"sender in the header is not the sender", returnReq{mutateMsg: func(m *iso.Pacs004) { m.AppHdr.Fr = iso.NewParty(beta) }}, "AGNT"},
		{"receiving participant not registered", returnReq{destinatario: "12345678"}, "RC10"},
		{"same participant", returnReq{destinatario: alfa}, "AG12"},
		{"count does not match", returnReq{mutateMsg: func(m *iso.Pacs004) { m.Document.PmtRtr.GrpHdr.NbOfTxs = "2" }}, "AM18"},
		{"payment that does not exist", returnReq{e2e: "E99000001202611141200ZZZZZZZZZZZ"}, "CH16"},
		{"sender did not receive the payment", returnReq{sender: beta, destinatario: wallet}, "CH16"},
		{"wrong receiving participant", returnReq{destinatario: beta}, "CH16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := paidByWallet(t)
			if tt.req.sender == "" {
				tt.req.sender = alfa
			}
			e.sendReturn(tt.req)
			id := tt.req.id
			if id == "" {
				id = rtr1
			}
			sts := e.readStatuses(tt.req.sender)
			if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != tt.want {
				t.Fatalf("statuses = %+v; want RJCT %s", sts, tt.want)
			}
			if sts[0].OrgnlInstrID != id || sts[0].OrgnlEndToEnd != e2eA && tt.req.e2e == "" {
				t.Fatalf("answer refers to %q / %q, want return %q of payment %q", sts[0].OrgnlInstrID, sts[0].OrgnlEndToEnd, id, e2eA)
			}
			e.noPayment(id)
			if n := len(e.read(wallet)); n != 0 {
				t.Fatalf("wallet received %d messages; a rejected return is not forwarded", n)
			}
		})
	}
}

func TestReturnNotSettled(t *testing.T) {
	e := newRulesEnv(t)
	e.sendOrder(order{sender: alfa, payee: wallet}) // the wallet has not answered: still pending
	e.read(wallet)
	e.sendReturn(returnReq{sender: wallet, destinatario: alfa})
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].ReasonCode() != "CH16" {
		t.Fatalf("statuses = %+v; want RJCT CH16", sts)
	}
}

func TestReturnAfter90Days(t *testing.T) {
	e := paidByWallet(t)
	e.advance(89 * 24 * time.Hour)
	late := "D99000002" + e.clock.Format("200601021504") + "RETURNLATE1"
	e.sendReturn(returnReq{id: late}) // still inside the limit
	if r := e.payment(late); r.Status == "" {
		t.Fatal("a return 89 days after the payment should be accepted")
	}

	e.advance(2 * 24 * time.Hour) // 91 days
	tooLate := "D99000002" + e.clock.Format("200601021504") + "RETURNLATE2"
	e.sendReturn(returnReq{id: tooLate})
	sts := e.readStatuses(alfa)
	if len(sts) != 1 || sts[0].ReasonCode() != "DT05" || sts[0].OrgnlInstrID != tooLate {
		t.Fatalf("statuses = %+v; want RJCT DT05", sts)
	}
	e.noPayment(tooLate)
}

func TestReturnOfReturn(t *testing.T) {
	e := paidByWallet(t)
	e.sendReturn(returnReq{})
	e.read(wallet)
	e.send(wallet, returnAnswer(t, wallet, rtr1, e2eA, iso.StatusAcceptedByReceiver, ""))
	e.read(wallet)
	e.read(alfa)

	// The wallet paid the original payment and now receives the return; it tries to send it back.
	again := "D99000001202611141200BACKAGAIN01"
	e.sendReturn(returnReq{id: again, sender: wallet, destinatario: alfa})
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].ReasonCode() != "AG13" {
		t.Fatalf("statuses = %+v; want RJCT AG13", sts)
	}
	e.noPayment(again)
}

// --- Idempotency and bad messages ---

func TestRepeatedReturn(t *testing.T) {
	e := paidToWallet(t)
	req := returnReq{sender: wallet, destinatario: alfa}
	e.sendReturn(req)
	e.readStatuses(wallet)

	e.sendReturn(req)
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer || sts[0].OrgnlInstrID != rtr1 {
		t.Fatalf("repeated return answered with %+v; want the ACSC again", sts)
	}
	// Settled once: the sum of returns is still 10.50.
	e.sendReturn(returnReq{id: rtr2, sender: wallet, destinatario: alfa, amount: "0.01"})
	if r := e.payment(rtr2); r.ReasonCode != "AM09" {
		t.Fatalf("return after the full amount = %+v; want AM09", r)
	}
}

func TestRepeatedPendingReturn(t *testing.T) {
	e := paidByWallet(t)
	e.sendReturn(returnReq{})
	e.sendReturn(returnReq{})
	if got := e.read(wallet); len(got) != 1 {
		t.Fatalf("wallet got %d returns, want 1", len(got))
	}
}

func TestReturnBadIDs(t *testing.T) {
	for name, req := range map[string]returnReq{
		"RtrId":      {id: "not-a-return-id"},
		"EndToEndId": {e2e: "not-an-end-to-end-id"},
	} {
		t.Run(name, func(t *testing.T) {
			e := paidByWallet(t)
			e.sendReturn(req)
			errs := e.readErrors(alfa)
			if len(errs) != 1 || errs[0].Reason() != "Identificador inválido" {
				t.Fatalf("errors = %+v; want one admi.002", errs)
			}
		})
	}
}

func TestReturnIDHelper(t *testing.T) {
	if !pix.ValidReturnID(rtr1) {
		t.Fatal("a well formed RtrId was refused")
	}
	for _, bad := range []string{"", "E99000002202611141200RETURN00001", "D99000002202611141200RETURN0000", "d99000002202611141200RETURN00001"} {
		if pix.ValidReturnID(bad) {
			t.Errorf("%q should not be a valid RtrId", bad)
		}
	}
}

// --- Idempotency window (24 hours) ---

func TestOrderIdempotencyWindow(t *testing.T) {
	e := newRulesEnv(t)
	e.sendOrder(order{})
	e.readStatuses(wallet)

	// 13 hours later the EndToEndId would already be too old for a new order, yet the same order still gets its answer.
	e.advance(13 * time.Hour)
	e.sendOrder(order{})
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer {
		t.Fatalf("after 13 hours: %+v; want the original ACSC again", sts)
	}

	// After 24 hours it is judged like a new order, and its EndToEndId is too old.
	e.advance(12 * time.Hour)
	e.sendOrder(order{})
	sts = e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "FF08" {
		t.Fatalf("after 25 hours: %+v; want RJCT FF08", sts)
	}
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("the original payment changed to %s", p.Status)
	}
}

func TestReturnIdempotencyWindow(t *testing.T) {
	e := paidToWallet(t)
	req := returnReq{sender: wallet, destinatario: alfa}
	e.sendReturn(req)
	e.readStatuses(wallet)

	e.advance(13 * time.Hour)
	e.sendReturn(req)
	if sts := e.readStatuses(wallet); len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer {
		t.Fatalf("after 13 hours: %+v; want the original ACSC again", sts)
	}

	e.advance(12 * time.Hour)
	e.sendReturn(req)
	if sts := e.readStatuses(wallet); len(sts) != 1 || sts[0].ReasonCode() != "FF08" {
		t.Fatalf("after 25 hours: %+v; want RJCT FF08", sts)
	}
}
