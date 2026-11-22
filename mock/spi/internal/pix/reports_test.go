package pix_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/pix"
)

const (
	e2eBook1 = "E99000001202611141155BOOKXFER001"
	e2eBook2 = "E99000001202611141156BOOKXFER002"
	rtrBook  = "D99000001202611141157BOOKRETURN1"
)

// report builds the trck.002 a participant sends. The default is the wallet reporting a payment from Ana Souza to
// Bruno Lima, two of its customers, which settled five minutes ago in its own ledger.
type report struct {
	sender    string
	msgID     string
	txs       []iso.TrackedTx
	mutateMsg func(*iso.Trck002)
}

func bookTx(e2e string, mutate ...func(*iso.TrackedTx)) iso.TrackedTx {
	tx := iso.NewTrackedTx(e2e, "25.90", iso.FormatTime(now.Add(-5*time.Minute)))
	tx.SetDebtor("52998224725", wallet, "0001", "00123456", "CACC")
	tx.SetCreditor("11144477735", wallet, "0001", "00234567", "SVGS", "")
	for _, m := range mutate {
		m(&tx)
	}
	return tx
}

func (r report) body(t *testing.T) []byte {
	t.Helper()
	if r.sender == "" {
		r.sender = wallet
	}
	if r.msgID == "" {
		r.msgID = "M" + r.sender + "reportABCDEFGHIJKLMNOPQ"
	}
	if r.txs == nil {
		r.txs = []iso.TrackedTx{bookTx(e2eBook1)}
	}
	msg := iso.NewTrck002(r.sender, pix.SPIISPB, r.msgID, iso.FormatTime(now), r.txs...)
	if r.mutateMsg != nil {
		r.mutateMsg(msg)
	}
	body, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (e *env) sendReport(r report) {
	e.t.Helper()
	if r.sender == "" {
		r.sender = wallet
	}
	e.send(r.sender, r.body(e.t))
}

// readReceipts returns, and acknowledges, the receipts (camt.025) queued for the participant.
func (e *env) readReceipts(ispb string) []iso.Receipt {
	e.t.Helper()
	var out []iso.Receipt
	for _, m := range e.read(ispb) {
		c, err := iso.ParseCamt025(m.Body)
		if err != nil || c.AppHdr.MsgDefIdr != iso.Camt025ID {
			e.t.Fatalf("message for %s is not a camt.025 (%v):\n%s", ispb, err, m.Body)
		}
		if c.AppHdr.Fr.ISPB() != pix.SPIISPB || c.AppHdr.To.ISPB() != ispb {
			e.t.Fatalf("header Fr %q To %q", c.AppHdr.Fr.ISPB(), c.AppHdr.To.ISPB())
		}
		out = append(out, c.Receipts()...)
	}
	return out
}

func (e *env) reported(id string) pix.ReportedTransaction {
	e.t.Helper()
	r, err := e.store.GetReported(context.Background(), id)
	if err != nil {
		e.t.Fatalf("reported transaction %s: %v", id, err)
	}
	return r
}

func (e *env) notReported(id string) {
	e.t.Helper()
	if _, err := e.store.GetReported(context.Background(), id); err == nil {
		e.t.Fatalf("transaction %s was recorded; a rejected one must not be", id)
	}
}

// --- Accepted ---

func TestReport(t *testing.T) {
	e := newRulesEnv(t)
	e.sendReport(report{})

	got := e.reported(e2eBook1)
	if got.Scenario != "BOK1" || got.Initiation != "MANU" || got.AmountCents != 2590 || got.PayerISPB != wallet || got.PayeeISPB != wallet ||
		got.PayerAccount != "00123456" || got.PayeeAccount != "00234567" || got.PayerTaxID != "52998224725" || got.ReportedBy != wallet ||
		!got.SettledAt.Equal(now.Add(-5*time.Minute)) {
		t.Fatalf("recorded = %+v", got)
	}
	rs := e.readReceipts(wallet)
	if len(rs) != 1 || rs[0].Status() != iso.ReceiptAccepted || rs[0].Ref() != e2eBook1 || rs[0].OrgnlMsgID.MsgID != "M"+wallet+"reportABCDEFGHIJKLMNOPQ" {
		t.Fatalf("receipts = %+v; want one ACPT for %s", rs, e2eBook1)
	}
	if p, err := e.store.GetPayment(context.Background(), e2eBook1); err == nil {
		t.Fatalf("a reported transaction became a payment: %+v", p)
	}
}

func TestReportReceipts(t *testing.T) {
	e := newRulesEnv(t)
	e.sendReport(report{txs: []iso.TrackedTx{
		bookTx(e2eBook1),
		bookTx(e2eBook2, func(tx *iso.TrackedTx) { tx.Amount.Value = "0.00" }), // rejected
		bookTx("E99000001202611141158BOOKXFER003"),
	}})
	rs := e.readReceipts(wallet)
	if len(rs) != 3 {
		t.Fatalf("receipts = %d, want 3", len(rs))
	}
	want := []struct{ ref, status, reason string }{
		{e2eBook1, "ACPT", ""}, {e2eBook2, "RJCT", "AM01"}, {"E99000001202611141158BOOKXFER003", "ACPT", ""},
	}
	for i, w := range want {
		if rs[i].Ref() != w.ref || rs[i].Status() != w.status || rs[i].ReasonCode() != w.reason {
			t.Errorf("receipt %d = %s %s %s, want %s %s %s", i, rs[i].Ref(), rs[i].Status(), rs[i].ReasonCode(), w.ref, w.status, w.reason)
		}
	}
	e.reported(e2eBook1)
	e.notReported(e2eBook2)
}

func TestReportKey(t *testing.T) {
	e := newRulesEnv(t)
	e.sendReport(report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
		tx.SetCreditor("11144477735", wallet, "0001", "00234567", "SVGS", "11144477735")
	})}})
	if got := e.reported(e2eBook1); got.Initiation != "DICT" || got.PayeeKey != "11144477735" {
		t.Fatalf("recorded = %+v", got)
	}
}

func TestReportedReturn(t *testing.T) {
	e := newRulesEnv(t)
	e.sendReport(report{txs: []iso.TrackedTx{bookTx(e2eBook1), bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.PmtID.InstrID = rtrBook })}})
	rs := e.readReceipts(wallet)
	if len(rs) != 2 || rs[0].Ref() != e2eBook1 || rs[1].Ref() != rtrBook || rs[1].Status() != iso.ReceiptAccepted {
		t.Fatalf("receipts = %+v; want the payment and its return, each under its own identifier", rs)
	}
	if got := e.reported(rtrBook); got.EndToEndID != e2eBook1 {
		t.Fatalf("recorded return = %+v", got)
	}
}

func TestReportTwice(t *testing.T) {
	e := newRulesEnv(t)
	e.sendReport(report{})
	e.readReceipts(wallet)
	first := e.reported(e2eBook1)

	e.advance(time.Hour)
	e.sendReport(report{msgID: "M" + wallet + "secondReportABCDEFGHIJK", txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.Amount.Value = "99.00" })}})
	rs := e.readReceipts(wallet)
	if len(rs) != 1 || rs[0].Status() != iso.ReceiptAccepted {
		t.Fatalf("receipts = %+v; a repeated report is accepted", rs)
	}
	if got := e.reported(e2eBook1); got != first {
		t.Fatalf("recorded = %+v; the first report must stay", got)
	}
}

func TestReportDeadline(t *testing.T) {
	e := newRulesEnv(t)
	e.sendReport(report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
		tx.ReqdExctnDt.DtTm = iso.FormatTime(now.Add(-29*24*time.Hour - 23*time.Hour))
	})}})
	if rs := e.readReceipts(wallet); len(rs) != 1 || rs[0].Status() != iso.ReceiptAccepted {
		t.Fatalf("receipts = %+v; 29 days and 23 hours is inside the 30 days", rs)
	}
}

// --- Rejected ---

func TestReportRejected(t *testing.T) {
	tests := []struct {
		name string
		rep  report
		want string
	}{
		{"older than 30 days", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.ReqdExctnDt.DtTm = iso.FormatTime(now.Add(-30*24*time.Hour - time.Minute))
		})}}, "FF08"},
		{"settled in the future", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.ReqdExctnDt.DtTm = iso.FormatTime(now.Add(time.Hour))
		})}}, "FF08"},
		{"invalid settlement time", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.ReqdExctnDt.DtTm = "yesterday" })}}, "FF08"},
		{"zero amount", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.Amount.Value = "0.00" })}}, "AM01"},
		{"invalid amount", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.Amount.Value = "1.234" })}}, "ED05"},
		{"wrong currency", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.Amount.Ccy = "USD" })}}, "ED05"},
		{"payer participant is not the sender", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.SetDebtor("52998224725", alfa, "0001", "00123456", "CACC")
		})}}, "RC09"},
		{"payer participant not registered", report{sender: "12345678", txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.SetDebtor("52998224725", "12345678", "0001", "00123456", "CACC")
		})}}, "RC09"},
		{"receiver participant not registered", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.SetCreditor("11144477735", "12345678", "0001", "00234567", "SVGS", "")
			tx.PmtScnro.Prtry = "BOK2"
		})}}, "RC10"},
		{"between participants that do not settle together", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.SetCreditor("39053344705", alfa, "0001", "00456789", "CACC", "")
			tx.PmtScnro.Prtry = "BOK2"
		})}}, "AG01"},
		{"BOK1 with different participants", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.SetCreditor("39053344705", alfa, "0001", "00456789", "CACC", "")
		})}}, "ED05"},
		{"BOK2 with the same participant", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.PmtScnro.Prtry = "BOK2" })}}, "ED05"},
		{"unknown scenario", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.PmtScnro.Prtry = "BOK3" })}}, "ED05"},
		{"unknown initiation", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.PmtTpInf.LclInstrm.Prtry = "XXXX" })}}, "ED05"},
		{"key with manual initiation", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.CreditorAcct.Prxy = &iso.Proxy{ID: "k"} })}}, "ED05"},
		{"DICT initiation without key", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.PmtTpInf.LclInstrm.Prtry = "DICT" })}}, "ED05"},
		{"return that was not manual", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) {
			tx.PmtID.InstrID = rtrBook
			tx.SetCreditor("11144477735", wallet, "0001", "00234567", "SVGS", "11144477735")
		})}}, "ED05:" + rtrBook},
		{"salary account receiver", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.CreditorAcct.Tp.Cd = "SLRY" })}}, "ED05"},
		{"invalid CPF", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.Debtor.Pty.ID.PrvtID.Othr.ID = "123" })}}, "ED05"},
		{"missing account", report{txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.DebtorAcct.ID.Othr.Number = "" })}}, "ED05"},
		{"invalid message date", report{mutateMsg: func(m *iso.Trck002) { m.Document.PmtStsTrckrRpt.GrpHdr.CreDtTm = "today" }}, "DT02"},
		{"status other than ACCC", report{mutateMsg: func(m *iso.Trck002) { m.Document.PmtStsTrckrRpt.TrckrStsAndTx.TxSts.Sts = "RJCT" }}, "ED05"},
		{"sender in the header is not the sender", report{mutateMsg: func(m *iso.Trck002) { m.AppHdr.Fr = iso.NewParty(alfa) }}, "RC09"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newRulesEnv(t)
			sender := tt.rep.sender
			if sender == "" {
				sender = wallet
			}
			// The receipt names the transaction by its RtrId when it is a return ("CODE:ref"), otherwise by its EndToEndId.
			code, ref, _ := strings.Cut(tt.want, ":")
			if ref == "" {
				ref = e2eBook1
			}
			e.sendReport(tt.rep)
			rs := e.readReceipts(sender)
			if len(rs) != 1 || rs[0].Status() != iso.ReceiptRejected || rs[0].ReasonCode() != code || rs[0].Ref() != ref {
				t.Fatalf("receipts = %+v; want RJCT %s for %s", rs, code, ref)
			}
			e.notReported(ref)
		})
	}
}

func TestReportOfSPIPayment(t *testing.T) {
	e := newRulesEnv(t)
	e.sendOrder(order{}) // wallet to Banco Alfa: settled through the SPI under e2eA
	e.read(wallet)
	e.read(alfa)

	e.sendReport(report{txs: []iso.TrackedTx{bookTx(e2eA)}})
	rs := e.readReceipts(wallet)
	if len(rs) != 1 || rs[0].Status() != iso.ReceiptRejected || rs[0].ReasonCode() != "ED05" {
		t.Fatalf("receipts = %+v; want RJCT ED05", rs)
	}
	e.notReported(e2eA)
}

func TestReportCount(t *testing.T) {
	// Zero transactions: nothing to answer per transaction, so the SPI sends nothing.
	e := newRulesEnv(t)
	e.sendReport(report{txs: []iso.TrackedTx{}})
	if got := e.read(wallet); len(got) != 0 {
		t.Fatalf("got %d messages for a report without transactions", len(got))
	}

	// 501 transactions: every one is rejected with AM18.
	txs := make([]iso.TrackedTx, 501)
	for i := range txs {
		txs[i] = bookTx(fmt.Sprintf("E99000001202611141155%011d", i))
	}
	e.sendReport(report{txs: txs})
	rs := e.readReceipts(wallet)
	if len(rs) != 501 || rs[0].ReasonCode() != "AM18" || rs[500].ReasonCode() != "AM18" {
		t.Fatalf("receipts = %d (first %q); want 501 rejected with AM18", len(rs), rs[0].ReasonCode())
	}
	e.notReported("E99000001202611141155" + fmt.Sprintf("%011d", 0))

	// 500 are fine.
	e.sendReport(report{txs: txs[:500], msgID: "M" + wallet + "fiveHundredABCDEFGHIJKL"})
	rs = e.readReceipts(wallet)
	if len(rs) != 500 || rs[499].Status() != iso.ReceiptAccepted {
		t.Fatalf("receipts = %d; want 500 accepted", len(rs))
	}
}

func TestReportBadIDs(t *testing.T) {
	for name, rep := range map[string]report{
		"MsgId":      {msgID: "not-a-message-id"},
		"EndToEndId": {txs: []iso.TrackedTx{bookTx("not-an-end-to-end-id")}},
		"InstrId":    {txs: []iso.TrackedTx{bookTx(e2eBook1, func(tx *iso.TrackedTx) { tx.PmtID.InstrID = "not-a-return-id" })}},
	} {
		t.Run(name, func(t *testing.T) {
			e := newRulesEnv(t)
			e.sendReport(rep)
			errs := e.readErrors(wallet)
			if len(errs) != 1 {
				t.Fatalf("errors = %+v; want one admi.002", errs)
			}
			e.notReported(e2eBook1)
		})
	}
}
