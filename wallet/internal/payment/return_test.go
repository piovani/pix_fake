package payment

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

func (f *fixture) receivedAndSettled(t *testing.T) Payment {
	t.Helper()
	ctx := context.Background()
	if err := f.svc.OnPaymentOrder(ctx, anaOrder()); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.OnStatus(ctx, credited(inboundID)); err != nil {
		t.Fatal(err)
	}
	p, found, err := f.store.ByEndToEnd(ctx, inboundID, Inbound)
	if err != nil || !found || p.Status != Settled {
		t.Fatalf("%+v, %v, %v", p, found, err)
	}
	return p
}

func requestID(t *testing.T) string {
	t.Helper()
	id, err := pix.NewRequestID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *fixture) giveBack(t *testing.T, orig Payment, cents int64) Payment {
	t.Helper()
	p, err := f.svc.Return(context.Background(), ReturnInput{PaymentID: orig.ID, AmountCents: cents, RequestID: requestID(t)})
	if err != nil {
		t.Fatalf("Return: %v", err)
	}
	return p
}

func (f *fixture) returnsSent(t *testing.T) []pix.PaymentReturn {
	t.Helper()
	var out []pix.PaymentReturn
	for _, b := range f.bank.bodies {
		if kind, _ := pix.MessageKind(b); kind == pix.KindPacs004 {
			rs, err := pix.ParsePacs004(b)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, rs...)
		}
	}
	return out
}

func returnStatus(rtrID, status, reason string) pix.TxStatus {
	return pix.TxStatus{InstrID: rtrID, EndToEndID: inboundID, Status: status, ReasonCode: reason}
}

func (f *fixture) returnedOf(t *testing.T, orig Payment) int64 {
	t.Helper()
	p, err := f.svc.Get(context.Background(), orig.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p.ReturnedCents
}

func TestReturn(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)
	if f.balanceOf(t, anaID) != 101050 {
		t.Fatal("setup")
	}

	p := f.giveBack(t, orig, 1050)
	if p.Type != TypeReturn || p.Direction != Outbound || p.Status != Pending || p.AmountCents != 1050 || p.OriginalPaymentID != orig.ID ||
		p.AccountID != anaID || p.ReservationID == "" || !p.Submitted() || p.Counterparty.Participant != bancoAlfa {
		t.Fatalf("return = %+v", p)
	}
	if b, _ := f.ledger.Balance(ctx, anaID); b.AvailableCents != 100000 || b.ReservedCents != 1050 {
		t.Errorf("balance while the SPI decides = %+v", b)
	}

	sent := f.returnsSent(t)
	if len(sent) != 1 || sent[0].ReturnID != p.EndToEndID || sent[0].EndToEndID != inboundID || sent[0].AmountCents != 1050 ||
		sent[0].To != bancoAlfa || sent[0].From != walletISPB || sent[0].ReasonCode != pix.ReturnReasonCustomer {
		t.Fatalf("pacs.004 = %+v", sent)
	}
	if got := f.returnedOf(t, orig); got != 1050 {
		t.Errorf("returned while in flight = %d", got)
	}

	if err := f.svc.OnStatus(ctx, returnStatus(p.EndToEndID, pix.StatusSettled, "")); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(ctx, p.ID)
	if got.Status != Settled || got.FinishedAt.IsZero() {
		t.Errorf("return = %+v", got)
	}
	if b, _ := f.ledger.Balance(ctx, anaID); b.AvailableCents != 100000 || b.ReservedCents != 0 {
		t.Errorf("balance = %+v", b)
	}
	if o, _ := f.svc.Get(ctx, orig.ID); o.Status != Settled || o.ReturnedCents != 1050 {
		t.Errorf("original = %+v", o)
	}
	if err := f.svc.OnStatus(ctx, returnStatus(p.EndToEndID, pix.StatusSettled, "")); err != nil {
		t.Fatal(err)
	}
	if b, _ := f.ledger.Balance(ctx, anaID); b.AvailableCents != 100000 {
		t.Errorf("a repeated status moved money: %+v", b)
	}
}

func TestReturnPartial(t *testing.T) {
	f := newFixture(t)
	orig := f.receivedAndSettled(t)

	f.giveBack(t, orig, 400)
	f.giveBack(t, orig, 650)
	if got := f.returnedOf(t, orig); got != 1050 {
		t.Errorf("returned = %d", got)
	}
	_, err := f.svc.Return(context.Background(), ReturnInput{PaymentID: orig.ID, AmountCents: 1, RequestID: requestID(t)})
	if !errors.Is(err, ErrReturnTooLarge) {
		t.Errorf("one centavo more: %v", err)
	}
	if b, _ := f.ledger.Balance(context.Background(), anaID); b.ReservedCents != 1050 {
		t.Errorf("a refused return held money: %+v", b)
	}
}

func TestReturnRejected(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)
	p := f.giveBack(t, orig, 1050)

	if err := f.svc.OnStatus(ctx, returnStatus(p.EndToEndID, pix.StatusRejected, "AM09")); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(ctx, p.ID)
	if got.Status != Rejected || got.RejectCode != "AM09" || got.RejectReason == "" {
		t.Fatalf("return = %+v", got)
	}
	if b, _ := f.ledger.Balance(ctx, anaID); b.AvailableCents != 101050 || b.ReservedCents != 0 {
		t.Errorf("balance = %+v", b)
	}
	if f.returnedOf(t, orig) != 0 {
		t.Error("a rejected return still counts")
	}
	if p := f.giveBack(t, orig, 1050); p.Status != Pending {
		t.Errorf("returning again: %+v", p)
	}
}

func TestReturnIdempotent(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)
	in := ReturnInput{PaymentID: orig.ID, AmountCents: 400, RequestID: requestID(t)}

	first, err := f.svc.Return(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.Return(ctx, in)
	if err != nil || again.ID != first.ID || again.EndToEndID != first.EndToEndID {
		t.Fatalf("repeat: %+v, %v", again, err)
	}
	if len(f.returnsSent(t)) != 1 {
		t.Errorf("sent %d returns", len(f.returnsSent(t)))
	}
	if b, _ := f.ledger.Balance(ctx, anaID); b.ReservedCents != 400 || f.returnedOf(t, orig) != 400 {
		t.Errorf("held twice: %+v", b)
	}

	in.AmountCents = 500
	if _, err := f.svc.Return(ctx, in); !errors.Is(err, ErrRequestConflict) {
		t.Errorf("another amount: %v", err)
	}
}

func TestReturnNotAllowed(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)

	sent := f.pay(t, f.resolve(t), 100)
	book := f.pay(t, f.resolveBook(t), 100)
	bookReceived, _, _ := f.store.ByEndToEnd(ctx, book.EndToEndID, Inbound)

	pendingOrder := anaOrder()
	pendingOrder.EndToEndID = "E99000002202611211200pendingpen1"
	if err := f.svc.OnPaymentOrder(ctx, pendingOrder); err != nil {
		t.Fatal(err)
	}
	pending, _, _ := f.store.ByEndToEnd(ctx, pendingOrder.EndToEndID, Inbound)

	for name, id := range map[string]string{"a payment sent": sent.ID, "a book transfer": bookReceived.ID, "a payment not settled yet": pending.ID} {
		if _, err := f.svc.Return(ctx, ReturnInput{PaymentID: id, AmountCents: 10, RequestID: requestID(t)}); !errors.Is(err, ErrNotReturnable) {
			t.Errorf("%s: %v", name, err)
		}
	}

	f.advance(91 * 24 * time.Hour)
	if _, err := f.svc.Return(ctx, ReturnInput{PaymentID: orig.ID, AmountCents: 10, RequestID: requestID(t)}); !errors.Is(err, ErrNotReturnable) {
		t.Errorf("older than 90 days: %v", err)
	}
}

func TestReturnValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)

	for name, in := range map[string]ReturnInput{
		"no payment":     {AmountCents: 10, RequestID: requestID(t)},
		"zero amount":    {PaymentID: orig.ID, RequestID: requestID(t)},
		"no request id":  {PaymentID: orig.ID, AmountCents: 10},
		"bad request id": {PaymentID: orig.ID, AmountCents: 10, RequestID: "req-1"},
	} {
		if _, err := f.svc.Return(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for _, id := range []string{"0a1b2c3d-9999-4000-8000-000000000009", "not-a-uuid"} {
		if _, err := f.svc.Return(ctx, ReturnInput{PaymentID: id, AmountCents: 10, RequestID: requestID(t)}); !errors.Is(err, ErrPaymentNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
}

func TestReturnNoFunds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)
	if err := f.ledger.Transfer(ctx, anaID, brunoID, 101050-500, "spent"); err != nil {
		t.Fatal(err)
	}

	_, err := f.svc.Return(ctx, ReturnInput{PaymentID: orig.ID, AmountCents: 1050, RequestID: requestID(t)})
	if !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if f.returnedOf(t, orig) != 0 || len(f.returnsSent(t)) != 0 {
		t.Error("a refused return left a trace")
	}
}

func TestReturnSPIDown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	orig := f.receivedAndSettled(t)
	f.bank.err = fmt.Errorf("%w: connection refused", pix.ErrUnavailable)

	p := f.giveBack(t, orig, 400)
	if p.Status != Pending || p.Submitted() {
		t.Fatalf("return = %+v", p)
	}
	f.advance(resubmitAfter + time.Second)
	f.bank.err = nil
	if n, err := f.svc.SendPending(ctx); err != nil || n != 1 {
		t.Fatalf("sent %d, %v", n, err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); !got.Submitted() {
		t.Errorf("return = %+v", got)
	}
	if sent := f.returnsSent(t); len(sent) != 1 || sent[0].ReturnID != p.EndToEndID {
		t.Errorf("pacs.004 = %+v", sent)
	}
}

func TestReturnSPIBadRequest(t *testing.T) {
	f := newFixture(t)
	orig := f.receivedAndSettled(t)
	f.bank.err = &pix.SPIError{Status: 400, Kind: "bad-request"}

	p := f.giveBack(t, orig, 400)
	if p.Status != Rejected || p.RejectCode == "" {
		t.Fatalf("return = %+v", p)
	}
	if b, _ := f.ledger.Balance(context.Background(), anaID); b.AvailableCents != 101050 || b.ReservedCents != 0 {
		t.Errorf("balance = %+v", b)
	}
}

func TestReturnConcurrent(t *testing.T) {
	f := newFixture(t)
	orig := f.receivedAndSettled(t)

	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = f.svc.Return(context.Background(), ReturnInput{PaymentID: orig.ID, AmountCents: 600, RequestID: requestID(t)})
		}()
	}
	wg.Wait()

	ok := 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrReturnTooLarge):
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || f.returnedOf(t, orig) != 600 {
		t.Errorf("%d returns, %d returned", ok, f.returnedOf(t, orig))
	}
	if b, _ := f.ledger.Balance(context.Background(), anaID); b.ReservedCents != 600 {
		t.Errorf("balance = %+v", b)
	}
}

func TestReturnConcurrentSameRequest(t *testing.T) {
	f := newFixture(t)
	orig := f.receivedAndSettled(t)
	in := ReturnInput{PaymentID: orig.ID, AmountCents: 1050, RequestID: requestID(t)}

	var wg sync.WaitGroup
	ids, errs := make([]string, 6), make([]error, 6)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.svc.Return(context.Background(), in)
			ids[i], errs[i] = p.ID, err
		}()
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("call %d: %q, %v (all: %v)", i, ids[i], errs[i], ids)
		}
	}
	if b, _ := f.ledger.Balance(context.Background(), anaID); b.ReservedCents != 1050 {
		t.Errorf("balance = %+v", b)
	}
}
