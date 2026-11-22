package payment

import (
	"context"
	"testing"
	"time"

	"pixfake/wallet/internal/pix"
)

const theirReturnID = "D99000002202611211205abcdefghijk"

func (f *fixture) paidAndSettled(t *testing.T, cents int64) Payment {
	t.Helper()
	p := f.pay(t, f.resolve(t), cents)
	if err := f.svc.OnStatus(context.Background(), settled(p.EndToEndID)); err != nil {
		t.Fatal(err)
	}
	return p
}

func returnOf(orig Payment, cents int64) pix.PaymentReturn {
	return pix.PaymentReturn{
		ReturnID: theirReturnID, EndToEndID: orig.EndToEndID, AmountCents: cents, From: bancoAlfa, To: walletISPB, Message: "refund",
	}
}

func theirReturnSettled(orig Payment) pix.TxStatus {
	return pix.TxStatus{
		InstrID: theirReturnID, EndToEndID: orig.EndToEndID, Status: pix.StatusCredit,
		SettledAt: time.Date(2026, 11, 21, 12, 5, 2, 0, time.UTC),
	}
}

func (f *fixture) lastAnswer(t *testing.T) pix.TxStatus {
	t.Helper()
	all := f.answers(t)
	if len(all) == 0 {
		t.Fatal("no answer was sent")
	}
	return all[len(all)-1]
}

func TestAReturnOfAPaymentTheWalletMadeCreditsThePayer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.paidAndSettled(t, 2590)
	before := f.balance(t)

	if err := f.svc.OnReturnOrder(ctx, returnOf(orig, 1000)); err != nil {
		t.Fatal(err)
	}
	if a := f.lastAnswer(t); a.Status != pix.StatusAccepted || a.InstrID != theirReturnID || a.EndToEndID != orig.EndToEndID {
		t.Fatalf("answer: %+v", a)
	}
	if got := f.balance(t); got != before {
		t.Fatalf("nothing is credited before the SPI settles: %+v, was %+v", got, before)
	}

	if err := f.svc.OnStatus(ctx, theirReturnSettled(orig)); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t); got.AvailableCents != before.AvailableCents+1000 {
		t.Fatalf("balance %+v, was %+v", got, before)
	}
	p, found, err := f.store.ByEndToEnd(ctx, theirReturnID, Inbound)
	if err != nil || !found {
		t.Fatalf("%v, %v", found, err)
	}
	if p.Type != TypeReturn || p.Status != Settled || p.OriginalPaymentID != orig.ID || p.AccountID != anaID || p.Counterparty.Name != "Carla Dias" {
		t.Fatalf("%+v", p)
	}
	if got := f.returnedOf(t, orig); got != 1000 {
		t.Fatalf("returned %d", got)
	}
}

func TestTheSameReturnTwiceCreditsOnce(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.paidAndSettled(t, 2590)
	before := f.balance(t)

	for range 2 {
		if err := f.svc.OnReturnOrder(ctx, returnOf(orig, 1000)); err != nil {
			t.Fatal(err)
		}
		if err := f.svc.OnStatus(ctx, theirReturnSettled(orig)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.svc.OnReturnOrder(ctx, returnOf(orig, 1000)); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t); got.AvailableCents != before.AvailableCents+1000 {
		t.Fatalf("balance %+v, was %+v", got, before)
	}
	if got := f.lastAnswer(t); got.Status != pix.StatusAccepted {
		t.Fatalf("a repeated return that settled is not refused: %+v", got)
	}
}

func TestAReturnTheWalletCannotMatchIsRefused(t *testing.T) {
	orig := func(f *fixture, t *testing.T) Payment { return f.paidAndSettled(t, 2590) }
	for name, tc := range map[string]struct {
		change func(*pix.PaymentReturn)
		reason string
	}{
		"unknown payment":     {func(r *pix.PaymentReturn) { r.EndToEndID = "E99000001202611210000zzzzzzzzzzz" }, "DS04"},
		"another participant": {func(r *pix.PaymentReturn) { r.From = "99000003" }, "DS04"},
		"above what was paid": {func(r *pix.PaymentReturn) { r.AmountCents = 2591 }, "AM09"},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			o := orig(f, t)
			before := f.balance(t)
			r := returnOf(o, 1000)
			tc.change(&r)

			if err := f.svc.OnReturnOrder(context.Background(), r); err != nil {
				t.Fatal(err)
			}
			if a := f.lastAnswer(t); a.Status != pix.StatusRejected || a.ReasonCode != tc.reason || a.InstrID != theirReturnID {
				t.Fatalf("answer: %+v", a)
			}
			if _, found, _ := f.store.ByEndToEnd(context.Background(), theirReturnID, Inbound); found {
				t.Fatal("a refused return is not recorded")
			}
			if got := f.balance(t); got != before {
				t.Fatalf("balance %+v, was %+v", got, before)
			}
		})
	}
}

func TestAReturnOfAPaymentNotSettledIsRefused(t *testing.T) {
	f := newFixture(t)
	pending := f.pay(t, f.resolve(t), 500)

	if err := f.svc.OnReturnOrder(context.Background(), returnOf(pending, 500)); err != nil {
		t.Fatal(err)
	}
	if a := f.lastAnswer(t); a.Status != pix.StatusRejected || a.ReasonCode != "DS04" {
		t.Fatalf("answer: %+v", a)
	}
}

func TestReturnsCannotAddUpToMoreThanWasPaid(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.paidAndSettled(t, 1000)

	if err := f.svc.OnReturnOrder(ctx, returnOf(orig, 700)); err != nil {
		t.Fatal(err)
	}
	second := returnOf(orig, 400)
	second.ReturnID = "D99000002202611211206abcdefghijk"
	if err := f.svc.OnReturnOrder(ctx, second); err != nil {
		t.Fatal(err)
	}
	if a := f.lastAnswer(t); a.Status != pix.StatusRejected || a.ReasonCode != "AM09" {
		t.Fatalf("answer: %+v", a)
	}
}

func TestARejectedReturnCreditsNothing(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.paidAndSettled(t, 1000)
	if err := f.svc.OnReturnOrder(ctx, returnOf(orig, 1000)); err != nil {
		t.Fatal(err)
	}
	before := f.balance(t)

	st := pix.TxStatus{InstrID: theirReturnID, EndToEndID: orig.EndToEndID, Status: pix.StatusRejected, ReasonCode: "AB03"}
	if err := f.svc.OnStatus(ctx, st); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t); got != before {
		t.Fatalf("balance %+v, was %+v", got, before)
	}
	p, _, _ := f.store.ByEndToEnd(ctx, theirReturnID, Inbound)
	if p.Status != Rejected || f.returnedOf(t, orig) != 0 {
		t.Fatalf("%+v, returned %d", p, f.returnedOf(t, orig))
	}
}

func TestAReturnComesThroughTheStream(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.paidAndSettled(t, 1000)
	before := f.balance(t)

	_, body, err := pix.NewPacs004(bancoAlfa, pix.PaymentReturn{
		ReturnID: theirReturnID, EndToEndID: orig.EndToEndID, AmountCents: 400, To: walletISPB,
	}, f.clock)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Handle(ctx, pix.Message{ResourceID: "r1", Body: body}); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.OnStatus(ctx, theirReturnSettled(orig)); err != nil {
		t.Fatal(err)
	}
	if got := f.balance(t); got.AvailableCents != before.AvailableCents+400 {
		t.Fatalf("balance %+v, was %+v", got, before)
	}
}
