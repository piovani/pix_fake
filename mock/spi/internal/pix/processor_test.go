package pix_test

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/spi"
)

const (
	wallet = "99000001" // real participant
	alfa   = "99000002" // simulated
	beta   = "99000003" // simulated

	e2eA = "E99000001202611141200AAAAAAAAAAA"
	e2eB = "E99000001202611141200BBBBBBBBBBB"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

type env struct {
	t     *testing.T
	store *memstore.Store
	svc   *spi.Service
	proc  *pix.Processor
	clock time.Time
}

type responderFunc func(pix.Payment) pix.Decision

func (f responderFunc) Decide(_ context.Context, p pix.Payment) pix.Decision { return f(p) }

// newEnv starts the SPI with the given responder (accept-all when nil).
func newEnv(t *testing.T, responder pix.Responder) *env {
	t.Helper()
	if responder == nil {
		responder = pix.AcceptAll{}
	}
	return newEnvWith(t, func(*memstore.Store) pix.Responder { return responder })
}

// newEnvWith builds the responder from the store, for the ones that read accounts.
func newEnvWith(t *testing.T, responder func(*memstore.Store) pix.Responder) *env {
	t.Helper()
	store := memstore.New()
	svc := spi.NewService(store, 0)
	proc := pix.NewProcessor(svc, store, pix.DefaultParticipants(), responder(store), slog.New(slog.NewTextHandler(io.Discard, nil)))
	e := &env{t: t, store: store, svc: svc, proc: proc, clock: now}
	proc.SetClock(func() time.Time { return e.clock })
	svc.SetHandler(proc)
	return e
}

// advance moves the SPI's clock forward.
func (e *env) advance(d time.Duration) { e.clock = e.clock.Add(d) }

// order builds the pacs.008 a participant sends. The default is the wallet paying Banco Alfa.
type order struct {
	e2e      string
	sender   string
	amount   string
	payer    string
	payee    string
	key      string
	purpose  string
	mutateTx func(*iso.CreditTransfer)
	mutate   func(*iso.Pacs008)
}

func (o order) body(t *testing.T) []byte {
	t.Helper()
	if o.e2e == "" {
		o.e2e = e2eA
	}
	if o.sender == "" {
		o.sender = wallet
	}
	if o.amount == "" {
		o.amount = "10.50"
	}
	if o.payer == "" {
		o.payer = o.sender
	}
	if o.payee == "" {
		o.payee = alfa
	}
	tx := iso.NewCreditTransfer(o.e2e, o.amount, iso.FormatTime(now.Add(-time.Second)))
	tx.SetDebtor("Ana Souza", "52998224725", o.payer, "0001", "00123456", "CACC")
	tx.SetCreditor("39053344705", o.payee, "0001", "00456789", "CACC", o.key)
	if o.purpose != "" {
		tx.Purpose.Cd = o.purpose
	}
	if o.mutateTx != nil {
		o.mutateTx(&tx)
	}
	msg := iso.NewPacs008(o.sender, pix.SPIISPB, "M"+o.sender+"abcdefghijklmnopqrstuvw", iso.FormatTime(now), "HIGH", "PAGPRI", tx)
	if o.mutate != nil {
		o.mutate(msg)
	}
	body, err := msg.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func (e *env) send(sender string, body []byte) {
	e.t.Helper()
	if _, err := e.svc.Receive(context.Background(), sender, [][]byte{body}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) sendOrder(o order) {
	e.t.Helper()
	if o.sender == "" {
		o.sender = wallet
	}
	e.send(o.sender, o.body(e.t))
}

// read returns, and acknowledges, everything queued for the participant.
func (e *env) read(ispb string) []spi.Message {
	e.t.Helper()
	ctx := context.Background()
	token, err := e.svc.StartStream(ctx, ispb)
	if err != nil {
		e.t.Fatal(err)
	}
	var all []spi.Message
	for {
		msgs, next, err := e.svc.Pull(ctx, ispb, token, spi.MaxMessages)
		if err != nil {
			e.t.Fatal(err)
		}
		token = next
		if len(msgs) == 0 {
			break
		}
		all = append(all, msgs...)
	}
	if err := e.svc.CloseStream(ctx, ispb, token); err != nil {
		e.t.Fatal(err)
	}
	return all
}

func (e *env) readStatuses(ispb string) []iso.TxStatus {
	e.t.Helper()
	var out []iso.TxStatus
	for _, m := range e.read(ispb) {
		p, err := iso.ParsePacs002(m.Body)
		if err != nil {
			e.t.Fatalf("message for %s is not a pacs.002: %v\n%s", ispb, err, m.Body)
		}
		hdr, _ := iso.Header(m.Body)
		if hdr.Fr.ISPB() != pix.SPIISPB || hdr.To.ISPB() != ispb {
			e.t.Fatalf("header Fr %q To %q; want %q to %q", hdr.Fr.ISPB(), hdr.To.ISPB(), pix.SPIISPB, ispb)
		}
		out = append(out, p.Statuses()...)
	}
	return out
}

func (e *env) payment(e2e string) pix.Payment {
	e.t.Helper()
	p, err := e.store.GetPayment(context.Background(), e2e)
	if err != nil {
		e.t.Fatalf("payment %s: %v", e2e, err)
	}
	return p
}

func (e *env) noPayment(e2e string) {
	e.t.Helper()
	if _, err := e.store.GetPayment(context.Background(), e2e); err == nil {
		e.t.Fatalf("payment %s exists; a rejected order must not be stored", e2e)
	}
}

// statusFrom builds the pacs.002 the receiving participant sends to the SPI.
func statusFrom(t *testing.T, from, e2e, status, reason string) []byte {
	t.Helper()
	st := iso.NewTxStatus(e2e, status)
	if reason != "" {
		st = st.WithReason(reason, "")
	}
	body, err := iso.NewPacs002(from, pix.SPIISPB, "M"+from+"abcdefghijklmnopqrstuvw", iso.FormatTime(now), st).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// --- Happy path ---

func TestPayBank(t *testing.T) {
	e := newEnv(t, nil)
	e.sendOrder(order{key: "39053344705"})

	p := e.payment(e2eA)
	if p.Status != pix.StatusSettled || p.AmountCents != 1050 || p.PayerISPB != wallet || p.PayeeISPB != alfa ||
		p.PayeeKey != "39053344705" || p.Initiation != "DICT" || !p.SettledAt.Equal(now) {
		t.Fatalf("payment = %+v", p)
	}
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer || sts[0].OrgnlEndToEnd != e2eA || sts[0].OrgnlInstrID != e2eA {
		t.Fatalf("payer statuses = %+v; want one ACSC", sts)
	}
	if sts[0].SettledAt == nil || sts[0].SettledAt.DtTm != "2026-11-14T12:00:00.000Z" ||
		sts[0].OrgnlTxRef == nil || sts[0].OrgnlTxRef.SettlementDate != "2026-11-14" {
		t.Fatalf("settlement date and time missing: %+v", sts[0])
	}
	if got := e.read(alfa); len(got) != 1 {
		t.Fatalf("the simulated receiver should get its confirmation (ACCC), got %d messages", len(got))
	}
}

func TestPayWaitsForReceiver(t *testing.T) {
	e := newEnv(t, nil)
	// Banco Alfa pays a wallet customer.
	e.sendOrder(order{sender: alfa, payee: wallet, e2e: e2eA})

	if p := e.payment(e2eA); p.Status != pix.StatusPending {
		t.Fatalf("status = %s; the payment must wait for the wallet", p.Status)
	}
	if got := e.readStatuses(alfa); len(got) != 0 {
		t.Fatalf("payer got %+v before the receiver answered", got)
	}

	// The wallet reads the order the SPI forwarded.
	in := e.read(wallet)
	if len(in) != 1 {
		t.Fatalf("wallet received %d messages, want the forwarded pacs.008", len(in))
	}
	fwd, err := iso.ParsePacs008(in[0].Body)
	if err != nil {
		t.Fatal(err)
	}
	tx := fwd.Transactions()[0]
	if fwd.AppHdr.Fr.ISPB() != pix.SPIISPB || fwd.AppHdr.To.ISPB() != wallet || fwd.AppHdr.MsgDefIdr != iso.Pacs008ID ||
		tx.PmtID.EndToEnd != e2eA || tx.Amount.Value != "10.50" || tx.CreditorAgent.ISPB() != wallet || tx.DebtorAgent.ISPB() != alfa {
		t.Fatalf("forwarded order = %+v (header %+v)", tx, fwd.AppHdr)
	}

	// The wallet accepts: the SPI settles and confirms to both.
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("status = %s, want SETTLED", p.Status)
	}
	if got := e.readStatuses(wallet); len(got) != 1 || got[0].Status != iso.StatusSettledReceiver {
		t.Fatalf("receiver statuses = %+v; want ACCC", got)
	}
	if got := e.readStatuses(alfa); len(got) != 1 || got[0].Status != iso.StatusSettledPayer {
		t.Fatalf("payer statuses = %+v; want ACSC", got)
	}
}

// --- Rejection by the receiving participant ---

func TestReceiverRejects(t *testing.T) {
	e := newEnv(t, nil)
	e.sendOrder(order{sender: alfa, payee: wallet})
	e.read(wallet)

	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusRejected, "AC07"))

	p := e.payment(e2eA)
	if p.Status != pix.StatusRejected || p.ReasonCode != "AC07" {
		t.Fatalf("payment = %+v", p)
	}
	sts := e.readStatuses(alfa)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AC07" || sts[0].SettledAt != nil {
		t.Fatalf("payer statuses = %+v; want RJCT AC07 without settlement", sts)
	}
	if got := e.read(wallet); len(got) != 0 {
		t.Fatalf("the rejecting receiver should not get a message back, got %d", len(got))
	}
}

func TestReceiverRejectsNoReason(t *testing.T) {
	e := newEnv(t, nil)
	e.sendOrder(order{sender: alfa, payee: wallet})
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusRejected, ""))
	if p := e.payment(e2eA); p.ReasonCode != "DS04" {
		t.Fatalf("reason = %q, want DS04", p.ReasonCode)
	}
}

func TestBankResponder(t *testing.T) {
	e := newEnv(t, responderFunc(func(pix.Payment) pix.Decision {
		return pix.Decision{ReasonCode: "AC06", Detail: "blocked"}
	}))
	e.sendOrder(order{})
	if p := e.payment(e2eA); p.Status != pix.StatusRejected || p.ReasonCode != "AC06" || p.ReasonDetail != "blocked" {
		t.Fatalf("payment = %+v", p)
	}
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AC06" {
		t.Fatalf("statuses = %+v", sts)
	}
}

// --- Answers that must not count ---

func TestAnswerFromWrongParticipant(t *testing.T) {
	e := newEnv(t, nil)
	e.sendOrder(order{sender: alfa, payee: wallet})
	e.read(wallet)

	e.send(beta, statusFrom(t, beta, e2eA, iso.StatusAcceptedByReceiver, ""))     // not the receiver
	e.send(wallet, statusFrom(t, wallet, e2eB, iso.StatusAcceptedByReceiver, "")) // unknown payment
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusSettledPayer, ""))       // not an answer a participant gives
	if p := e.payment(e2eA); p.Status != pix.StatusPending {
		t.Fatalf("status = %s; none of those answers may settle the payment", p.Status)
	}
}

func TestSecondAnswer(t *testing.T) {
	e := newEnv(t, nil)
	e.sendOrder(order{sender: alfa, payee: wallet})
	e.read(wallet)
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))
	e.read(wallet)
	e.readStatuses(alfa)

	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusRejected, "DS04"))
	e.send(wallet, statusFrom(t, wallet, e2eA, iso.StatusAcceptedByReceiver, ""))
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("status = %s; a settled payment cannot change", p.Status)
	}
	if n := len(e.read(wallet)) + len(e.read(alfa)); n != 0 {
		t.Fatalf("%d extra messages sent after the payment was final", n)
	}
}

// --- Idempotency ---

func TestRepeatedOrder(t *testing.T) {
	e := newEnv(t, nil)
	o := order{}
	e.sendOrder(o)
	e.readStatuses(wallet)

	e.sendOrder(o)
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusSettledPayer {
		t.Fatalf("repeated order answered with %+v; want the ACSC again", sts)
	}
	if p := e.payment(e2eA); p.Status != pix.StatusSettled {
		t.Fatalf("payment = %+v", p)
	}
}

func TestRepeatedPendingOrder(t *testing.T) {
	e := newEnv(t, nil)
	o := order{sender: alfa, payee: wallet}
	e.sendOrder(o)
	e.sendOrder(o)
	if got := e.read(wallet); len(got) != 1 {
		t.Fatalf("wallet got %d orders, want 1", len(got))
	}
}

func TestRepeatedRejectedOrder(t *testing.T) {
	e := newEnv(t, responderFunc(func(pix.Payment) pix.Decision { return pix.Decision{ReasonCode: "AC03"} }))
	o := order{}
	e.sendOrder(o)
	e.readStatuses(wallet)
	e.sendOrder(o)
	sts := e.readStatuses(wallet)
	if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != "AC03" {
		t.Fatalf("statuses = %+v", sts)
	}
}

// --- Validation by the SPI ---

func TestOrderRejected(t *testing.T) {
	tests := []struct {
		name string
		o    order
		want string
	}{
		{"zero amount", order{amount: "0.00"}, "AM01"},
		{"amount with three decimals", order{amount: "1.234"}, "CH16"},
		{"negative amount", order{amount: "-5.00"}, "CH16"},
		{"wrong currency", order{mutateTx: func(tx *iso.CreditTransfer) { tx.Amount.Ccy = "USD" }}, "CH16"},
		{"book transfer", order{payee: wallet}, "AG12"},
		{"receiver not registered", order{payee: "12345678"}, "RC10"},
		{"payer participant not the sender", order{payer: alfa}, "AGNT"},
		{"sender in the header is not the sender", order{mutate: func(m *iso.Pacs008) { m.AppHdr.Fr = iso.NewParty(alfa) }}, "AGNT"},
		{"count does not match", order{mutate: func(m *iso.Pacs008) { m.Document.FIToFICstmrCdtTrf.GrpHdr.NbOfTxs = "2" }}, "AM18"},
		{"purpose not supported", order{purpose: "OTHR"}, "CH16"},
		{"normal priority", order{mutate: func(m *iso.Pacs008) { m.Document.FIToFICstmrCdtTrf.GrpHdr.PmtTpInf.Priority = "NORM" }}, "CH16"},
		{"scheduled", order{mutate: func(m *iso.Pacs008) { m.Document.FIToFICstmrCdtTrf.GrpHdr.PmtTpInf.SvcLvl.Proprietary = "PAGAGD" }}, "CH16"},
		{"DICT without key", order{mutateTx: func(tx *iso.CreditTransfer) { tx.Mandate.Tp.LclInstrm.Prtry = "DICT" }}, "CH16"},
		{"manual entry with key", order{mutateTx: func(tx *iso.CreditTransfer) {
			tx.CreditorAcct.Prxy = &iso.Proxy{ID: "k"}
		}}, "CH16"},
		{"QR code initiation", order{mutateTx: func(tx *iso.CreditTransfer) { tx.Mandate.Tp.LclInstrm.Prtry = "QRES" }}, "CH16"},
		{"salary account receiver", order{mutateTx: func(tx *iso.CreditTransfer) { tx.CreditorAcct.Tp.Cd = "SLRY" }}, "AG03"},
		{"unknown account type", order{mutateTx: func(tx *iso.CreditTransfer) { tx.CreditorAcct.Tp.Cd = "XXXX" }}, "CH16"},
		{"invalid CPF", order{mutateTx: func(tx *iso.CreditTransfer) { tx.Creditor.ID.PrvtID.Othr.ID = "123" }}, "CH16"},
		{"empty payer name", order{mutateTx: func(tx *iso.CreditTransfer) { tx.Debtor.Name = "" }}, "CH16"},
		{"branch with letters", order{mutateTx: func(tx *iso.CreditTransfer) { tx.CreditorAcct.ID.Othr.Branch = "AB12" }}, "CH16"},
		{"charge bearer", order{mutateTx: func(tx *iso.CreditTransfer) { tx.ChrgBr = "DEBT" }}, "CH16"},
		{"end to end 13 hours old", order{e2e: "E99000001202611132300CCCCCCCCCCC"}, "FF08"},
		{"end to end 13 hours ahead", order{e2e: "E99000001202611150100DDDDDDDDDDD"}, "FF08"},
		{"end to end with an impossible hour", order{e2e: "E99000001202611142500GGGGGGGGGGG"}, "FF08"},
		{"split payment block", order{mutateTx: func(tx *iso.CreditTransfer) {
			tx.Tax = &iso.RawXML{Inner: "<RefNb>x</RefNb>"}
		}}, "CH16"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEnv(t, nil)
			if tt.o.e2e == "" {
				tt.o.e2e = e2eA
			}
			e.sendOrder(tt.o)
			sts := e.readStatuses(wallet)
			if len(sts) != 1 || sts[0].Status != iso.StatusRejected || sts[0].ReasonCode() != tt.want {
				t.Fatalf("statuses = %+v; want RJCT %s", sts, tt.want)
			}
			if sts[0].OrgnlEndToEnd != tt.o.e2e {
				t.Fatalf("answered for %q, want %q", sts[0].OrgnlEndToEnd, tt.o.e2e)
			}
			e.noPayment(tt.o.e2e)
		})
	}
}

func TestEndToEndTolerance(t *testing.T) {
	e := newEnv(t, nil)
	e.sendOrder(order{e2e: "E99000001202611140100EEEEEEEEEEE"}) // 11 hours ago
	e.sendOrder(order{e2e: "E99000001202611142300FFFFFFFFFFF"}) // 11 hours ahead
	for _, id := range []string{"E99000001202611140100EEEEEEEEEEE", "E99000001202611142300FFFFFFFFFFF"} {
		if p := e.payment(id); p.Status != pix.StatusSettled {
			t.Fatalf("%s = %s", id, p.Status)
		}
	}
}

// readErrors returns, and acknowledges, the admi.002 notifications queued for the participant.
func (e *env) readErrors(ispb string) []*iso.Admi002 {
	e.t.Helper()
	var out []*iso.Admi002
	for _, m := range e.read(ispb) {
		a, err := iso.ParseAdmi002(m.Body)
		if err != nil || a.AppHdr.MsgDefIdr != iso.Admi002ID {
			e.t.Fatalf("message for %s is not an admi.002 (%v):\n%s", ispb, err, m.Body)
		}
		if a.AppHdr.Fr.ISPB() != pix.SPIISPB || a.AppHdr.To.ISPB() != ispb {
			e.t.Fatalf("header Fr %q To %q", a.AppHdr.Fr.ISPB(), a.AppHdr.To.ISPB())
		}
		out = append(out, a)
	}
	return out
}

func TestBadEndToEnd(t *testing.T) {
	e := newEnv(t, nil)
	ids, err := e.svc.Receive(context.Background(), wallet, [][]byte{order{e2e: "not-an-end-to-end-id"}.body(t)})
	if err != nil {
		t.Fatal(err)
	}
	errs := e.readErrors(wallet)
	if len(errs) != 1 || errs[0].Ref() != ids[0] || errs[0].Reason() != "Identificador fim a fim inválido" {
		t.Fatalf("errors = %+v; want one admi.002 referencing %s", errs, ids[0])
	}
}

func TestUnprocessable(t *testing.T) {
	e := newEnv(t, nil)
	bodies := [][]byte{
		[]byte("<not-iso/>"),
		[]byte("not xml at all"),
		[]byte(`<Envelope><AppHdr><MsgDefIdr>camt.060.spi.1.9</MsgDefIdr></AppHdr></Envelope>`),
		[]byte(`<Envelope><AppHdr><MsgDefIdr>pacs.008.spi.1.16</MsgDefIdr></AppHdr><Document><x></Envelope>`),
	}
	ids, err := e.svc.Receive(context.Background(), wallet, bodies)
	if err != nil {
		t.Fatal(err)
	}
	errs := e.readErrors(wallet)
	if len(errs) != len(bodies) {
		t.Fatalf("got %d notifications, want %d", len(errs), len(bodies))
	}
	for i, a := range errs {
		if a.Ref() != ids[i] {
			t.Errorf("notification %d references %q, want the PI-ResourceId %q", i, a.Ref(), ids[i])
		}
	}
	if errs[2].Reason() != "Mensagem não suportada" {
		t.Errorf("unsupported type reason = %q", errs[2].Reason())
	}
	if n := len(e.store.Inbound()); n != len(bodies) {
		t.Fatalf("stored %d messages; every message sent is kept even when it cannot be processed", n)
	}
}

func TestManyTransactions(t *testing.T) {
	e := newEnv(t, nil)
	body := order{}.body(t)
	msg, _ := iso.ParsePacs008(body)
	second := msg.Transactions()[0]
	second.PmtID.EndToEnd = e2eB
	msg.Document.FIToFICstmrCdtTrf.CdtTrfTxInf = append(msg.Document.FIToFICstmrCdtTrf.CdtTrfTxInf, second)
	msg.Document.FIToFICstmrCdtTrf.GrpHdr.NbOfTxs = "2"
	body, _ = msg.Marshal()
	e.send(wallet, body)

	for _, id := range []string{e2eA, e2eB} {
		if p := e.payment(id); p.Status != pix.StatusSettled {
			t.Fatalf("%s = %s", id, p.Status)
		}
	}
	if sts := e.readStatuses(wallet); len(sts) != 2 {
		t.Fatalf("statuses = %d, want one per transaction", len(sts))
	}
}

func TestAmountHelpers(t *testing.T) {
	for in, want := range map[string]int64{"10": 1000, "10.5": 1050, "10.50": 1050, "0.01": 1, "123456789012345.99": 12345678901234599} {
		if got, ok := pix.ParseAmount(in); !ok || got != want {
			t.Errorf("ParseAmount(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "-1", "1.234", "abc", "1,50", "12345678901234567.99"} {
		if _, ok := pix.ParseAmount(in); ok {
			t.Errorf("ParseAmount(%q) should fail", in)
		}
	}
}
