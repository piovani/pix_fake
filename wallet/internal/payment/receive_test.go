package payment

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

const (
	bancoAlfa = "99000002"
	carlosTax = "39053344705"
	inboundID = "E99000002202611211200abcdefghijk"
)

func anaOrder() pix.PaymentOrder {
	return pix.PaymentOrder{
		EndToEndID:  inboundID,
		AmountCents: 1050,
		Payer:       pix.Party{ISPB: bancoAlfa, Name: "Carlos Mendes", TaxID: carlosTax, Branch: "0001", Account: "00456789", AccountType: pix.AccountChecking},
		Receiver:    pix.Party{ISPB: walletISPB, TaxID: anaTaxID, Branch: "0001", Account: "00123456", AccountType: pix.AccountChecking},
		Message:     "for lunch",
	}
}

func (f *fixture) answers(t *testing.T) []pix.TxStatus {
	t.Helper()
	var out []pix.TxStatus
	for _, b := range f.bank.bodies {
		sts, err := pix.ParsePacs002(b)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sts...)
	}
	return out
}

func (f *fixture) received(t *testing.T) []Payment {
	t.Helper()
	page, err := f.svc.List(context.Background(), ListInput{AccountID: anaID})
	if err != nil {
		t.Fatal(err)
	}
	var in []Payment
	for _, p := range page.Payments {
		if p.Direction == Inbound {
			in = append(in, p)
		}
	}
	return in
}

func credited(e2e string) pix.TxStatus {
	return pix.TxStatus{InstrID: e2e, EndToEndID: e2e, Status: pix.StatusCredit, SettledAt: time.Date(2026, 11, 21, 12, 0, 2, 0, time.UTC)}
}

func TestAcceptedPaymentIsCreditedWhenTheSPISettlesIt(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err != nil {
		t.Fatal(err)
	}
	sts := f.answers(t)
	if len(sts) != 1 || sts[0].Status != pix.StatusAccepted || sts[0].EndToEndID != inboundID || sts[0].InstrID != inboundID {
		t.Fatalf("answers = %+v", sts)
	}
	in := f.received(t)
	if len(in) != 1 || in[0].Status != Pending || in[0].Type != TypePix || in[0].AmountCents != 1050 || in[0].EndToEndID != inboundID || in[0].Message != "for lunch" {
		t.Fatalf("received = %+v", in)
	}
	if c := in[0].Counterparty; c.Name != "Carlos Mendes" || c.TaxID != carlosTax || c.Participant != bancoAlfa || c.AccountNumber != "00456789" {
		t.Errorf("payer = %+v", c)
	}
	if f.balanceOf(t, anaID) != 100000 {
		t.Fatal("credited before the SPI settled it")
	}

	if err := f.svc.OnStatus(ctx, credited(inboundID)); err != nil {
		t.Fatal(err)
	}
	if f.balanceOf(t, anaID) != 101050 {
		t.Errorf("Ana has %d", f.balanceOf(t, anaID))
	}
	got, _ := f.svc.Get(ctx, in[0].ID)
	if got.Status != Settled || !got.FinishedAt.Equal(time.Date(2026, 11, 21, 12, 0, 2, 0, time.UTC)) {
		t.Errorf("payment = %+v", got)
	}

	if err := f.svc.OnStatus(ctx, credited(inboundID)); err != nil {
		t.Fatal(err)
	}
	if f.balanceOf(t, anaID) != 101050 {
		t.Error("a repeated ACCC credited twice")
	}
}

func TestPaymentOrdersTheWalletCannotTake(t *testing.T) {
	for name, tt := range map[string]struct {
		mutate func(*pix.PaymentOrder)
		code   string
	}{
		"no such account":  {func(o *pix.PaymentOrder) { o.Receiver.Account = "99999999" }, "AC03"},
		"no such branch":   {func(o *pix.PaymentOrder) { o.Receiver.Branch = "0999" }, "AC03"},
		"another tax id":   {func(o *pix.PaymentOrder) { o.Receiver.TaxID = carlosTax }, "BE01"},
		"another type":     {func(o *pix.PaymentOrder) { o.Receiver.AccountType = pix.AccountSavings }, "AC14"},
		"a salary account": {func(o *pix.PaymentOrder) { o.Receiver.AccountType = pix.AccountSalary }, "AC14"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			o := anaOrder()
			tt.mutate(&o)

			if err := f.svc.OnPaymentOrder(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			sts := f.answers(t)
			if len(sts) != 1 || !sts[0].Rejected() || sts[0].ReasonCode != tt.code || sts[0].EndToEndID != inboundID {
				t.Fatalf("answers = %+v", sts)
			}
			if len(f.received(t)) != 0 || f.balanceOf(t, anaID) != 100000 {
				t.Error("a refused payment left a trace")
			}
		})
	}
}

func TestTheSameOrderDeliveredAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for range 3 {
		if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.received(t)) != 1 {
		t.Fatalf("recorded %d payments", len(f.received(t)))
	}
	for _, st := range f.answers(t) {
		if st.Status != pix.StatusAccepted {
			t.Errorf("answer = %+v", st)
		}
	}

	if err := f.svc.OnStatus(ctx, credited(inboundID)); err != nil {
		t.Fatal(err)
	}
	before := len(f.answers(t))
	if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err != nil {
		t.Fatal(err)
	}
	if len(f.answers(t)) != before || f.balanceOf(t, anaID) != 101050 {
		t.Error("an order already settled was answered or credited again")
	}
}

func TestAnswerThatCannotBeSentIsRetried(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.bank.err = fmt.Errorf("%w: connection refused", pix.ErrUnavailable)

	if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err == nil {
		t.Fatal("the failure must reach the caller, so the message is delivered again")
	}
	f.bank.err = nil
	if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err != nil {
		t.Fatal(err)
	}
	if sts := f.answers(t); len(sts) != 1 || sts[0].Status != pix.StatusAccepted {
		t.Errorf("answers = %+v", sts)
	}
	if len(f.received(t)) != 1 {
		t.Errorf("recorded %d payments", len(f.received(t)))
	}
}

func TestSPIRejectsAPaymentTheWalletAccepted(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err != nil {
		t.Fatal(err)
	}

	if err := f.svc.OnStatus(ctx, pix.TxStatus{InstrID: inboundID, EndToEndID: inboundID, Status: pix.StatusRejected, ReasonCode: "AB03"}); err != nil {
		t.Fatal(err)
	}
	in := f.received(t)
	if len(in) != 1 || in[0].Status != Rejected || in[0].RejectCode != "AB03" || in[0].RejectReason == "" {
		t.Fatalf("received = %+v", in)
	}
	if err := f.svc.OnStatus(ctx, credited(inboundID)); err != nil {
		t.Fatal(err)
	}
	if f.balanceOf(t, anaID) != 100000 {
		t.Error("credited a payment the SPI had rejected")
	}
}

func TestStatusOfAPaymentWeNeverSawIsIgnored(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.OnStatus(context.Background(), credited("E99000002202611211200zzzzzzzzzzz")); err != nil {
		t.Fatal(err)
	}
	if f.balanceOf(t, anaID) != 100000 {
		t.Error("money appeared")
	}
}

func TestHandleAnswersAPaymentOrderFromTheStream(t *testing.T) {
	f := newFixture(t)
	_, body, err := pix.NewPacs008(bancoAlfa, anaOrder(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Handle(context.Background(), pix.Message{ResourceID: "r", Body: body}); err != nil {
		t.Fatal(err)
	}
	if sts := f.answers(t); len(sts) != 1 || sts[0].Status != pix.StatusAccepted || sts[0].EndToEndID != inboundID {
		t.Errorf("answers = %+v", sts)
	}
	if len(f.received(t)) != 1 {
		t.Errorf("recorded %d payments", len(f.received(t)))
	}
}

func TestPaymentsToABlockedOrClosedAccountAreRefused(t *testing.T) {
	for status, reason := range map[ledger.AccountStatus]string{ledger.Blocked: "AC06", ledger.Closed: "AC07"} {
		t.Run(string(status), func(t *testing.T) {
			f := newFixture(t)
			if err := f.ledger.SetStatus(context.Background(), anaID, status); err != nil {
				t.Fatal(err)
			}
			if err := f.svc.OnPaymentOrder(context.Background(), anaOrder()); err != nil {
				t.Fatal(err)
			}
			if a := f.lastAnswer(t); a.Status != pix.StatusRejected || a.ReasonCode != reason {
				t.Fatalf("answer: %+v", a)
			}
			if got := f.received(t); len(got) != 0 {
				t.Fatalf("a refused payment is not recorded: %+v", got)
			}
		})
	}
}

func TestABlockedAccountCannotPay(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolve(t)
	if err := f.ledger.SetStatus(ctx, anaID, ledger.Blocked); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 100}); !errors.Is(err, ledger.ErrAccountNotActive) {
		t.Fatalf("got %v", err)
	}
	if f.bank.sent() != 0 {
		t.Fatal("nothing may go to the SPI")
	}
}
