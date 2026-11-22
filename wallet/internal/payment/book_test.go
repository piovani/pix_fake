package payment

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
)

var betweenTags = regexp.MustCompile(`>\s+<`)

func compact(b []byte) string { return betweenTags.ReplaceAllString(string(b), "><") }

func (f *fixture) resolveBook(t *testing.T) Resolution {
	t.Helper()
	r, err := f.svc.ResolveKey(context.Background(), anaID, brunoKey)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *fixture) balanceOf(t *testing.T, id string) int64 {
	t.Helper()
	b, err := f.ledger.Balance(context.Background(), id)
	if err != nil || b.ReservedCents != 0 {
		t.Fatalf("balance of %s: %+v, %v", id, b, err)
	}
	return b.AvailableCents
}

func TestResolveOwnKey(t *testing.T) {
	f := newFixture(t)
	r := f.resolveBook(t)

	if !r.BookTransfer || r.PayeeAccountID != brunoID || r.AccountID != anaID || r.ID == "" || len(r.EndToEndID) != 32 {
		t.Fatalf("resolution = %+v", r)
	}
	want := Payee{Name: "Bruno Lima", TaxID: brunoTaxID, Participant: walletISPB, Branch: "0001", AccountNumber: "00234567", AccountType: pix.AccountSavings, Key: brunoKey}
	if r.Payee != want {
		t.Errorf("payee = %+v, want %+v", r.Payee, want)
	}
	if len(f.dict.asked) != 0 {
		t.Errorf("the DICT was asked about a key of the wallet: %+v", f.dict.asked)
	}
	if got, found, err := f.store.Resolution(context.Background(), r.ID); err != nil || !found || !got.BookTransfer || got.PayeeAccountID != brunoID {
		t.Errorf("stored: %+v, %v, %v", got, found, err)
	}
}

func TestResolvePayerKey(t *testing.T) {
	f := newFixture(t)
	if _, err := f.svc.ResolveKey(context.Background(), anaID, anaKey); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if len(f.dict.asked) != 0 {
		t.Error("the DICT was asked")
	}
}

func TestBookTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolveBook(t)

	p := f.pay(t, r, 2590)
	if p.Status != Settled || p.Type != TypeBookTransfer || p.Direction != Outbound || p.AmountCents != 2590 || p.EndToEndID != r.EndToEndID ||
		p.FinishedAt.IsZero() || p.ReservationID != "" || p.Message != "lunch" {
		t.Fatalf("payment = %+v", p)
	}
	if got := f.balanceOf(t, anaID); got != 100000-2590 {
		t.Errorf("Ana has %d", got)
	}
	if got := f.balanceOf(t, brunoID); got != 50000+2590 {
		t.Errorf("Bruno has %d", got)
	}
	if f.bank.sent() != 0 || len(f.dict.asked) != 0 {
		t.Errorf("a book transfer reached the SPI (%d messages) or the DICT (%d lookups)", f.bank.sent(), len(f.dict.asked))
	}

	sent, err := f.svc.List(ctx, ListInput{AccountID: anaID})
	if err != nil || len(sent.Payments) != 1 || sent.Payments[0].ID != p.ID || sent.Payments[0].Direction != Outbound {
		t.Fatalf("Ana's payments: %+v, %v", sent, err)
	}
	got, err := f.svc.List(ctx, ListInput{AccountID: brunoID})
	if err != nil || len(got.Payments) != 1 {
		t.Fatalf("Bruno's payments: %+v, %v", got, err)
	}
	in := got.Payments[0]
	if in.Direction != Inbound || in.Type != TypeBookTransfer || in.Status != Settled || in.AmountCents != 2590 || in.EndToEndID != r.EndToEndID ||
		in.Message != "lunch" || in.Counterparty.Name != "Ana Souza" || in.Counterparty.TaxID != anaTaxID || in.FinishedAt.IsZero() || in.ID == p.ID {
		t.Errorf("Bruno's payment = %+v", in)
	}

	again := f.pay(t, r, 2590)
	if again.ID != p.ID || f.balanceOf(t, anaID) != 100000-2590 || f.balanceOf(t, brunoID) != 50000+2590 {
		t.Errorf("a repeat moved money again: %+v", again)
	}
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 1, Message: "lunch"}); !errors.Is(err, ErrResolutionConflict) {
		t.Errorf("another amount: %v", err)
	}
}

func TestBookTransferNoFunds(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolveBook(t)

	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 100001}); !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if f.balanceOf(t, anaID) != 100000 || f.balanceOf(t, brunoID) != 50000 {
		t.Error("a refused payment moved money")
	}
	if _, found, _ := f.store.ByResolution(ctx, r.ID); found {
		t.Error("the refused attempt left a payment behind")
	}
	if page, _ := f.svc.List(ctx, ListInput{AccountID: brunoID}); len(page.Payments) != 0 {
		t.Error("the payee sees a payment that did not happen")
	}

	if err := f.ledger.Credit(ctx, anaID, 5000, "deposit"); err != nil {
		t.Fatal(err)
	}
	if p := f.pay(t, r, 100001); p.Status != Settled {
		t.Errorf("payment = %+v", p)
	}
}

func TestBookTransferConcurrent(t *testing.T) {
	f := newFixture(t)
	r := f.resolveBook(t)

	var wg sync.WaitGroup
	ids, errs := make([]string, 8), make([]error, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p, err := f.svc.Pay(context.Background(), PayInput{ResolutionID: r.ID, AmountCents: 1000, Message: "x"})
			ids[i], errs[i] = p.ID, err
		}()
	}
	wg.Wait()
	for i, id := range ids {
		if errs[i] != nil || id != ids[0] {
			t.Fatalf("call %d: %q, %v (all: %v)", i, id, errs[i], ids)
		}
	}
	if f.balanceOf(t, anaID) != 99000 || f.balanceOf(t, brunoID) != 51000 {
		t.Errorf("Ana %d, Bruno %d", f.balanceOf(t, anaID), f.balanceOf(t, brunoID))
	}
	if page, _ := f.svc.List(context.Background(), ListInput{AccountID: brunoID}); len(page.Payments) != 1 {
		t.Errorf("Bruno received %d payments", len(page.Payments))
	}
}

// stopHalfway leaves a book transfer pending after its money moved.
func (f *fixture) stopHalfway(t *testing.T, cents int64) (Resolution, Payment) {
	t.Helper()
	ctx := context.Background()
	r := f.resolveBook(t)
	p, created, err := f.store.Insert(ctx, Payment{
		AccountID: anaID, Direction: Outbound, Type: TypeBookTransfer, Status: Pending, AmountCents: cents, EndToEndID: r.EndToEndID,
		ResolutionID: r.ID, Counterparty: r.Payee, Message: "lunch", CreatedAt: f.clock,
	})
	if err != nil || !created {
		t.Fatalf("%v, %v", created, err)
	}
	if err := f.ledger.Transfer(ctx, anaID, brunoID, cents, r.EndToEndID); err != nil {
		t.Fatal(err)
	}
	return r, p
}

func TestBookTransferResumeOnRetry(t *testing.T) {
	f := newFixture(t)
	r, p := f.stopHalfway(t, 2590)

	again := f.pay(t, r, 2590)
	if again.ID != p.ID || again.Status != Settled {
		t.Fatalf("payment = %+v", again)
	}
	if f.balanceOf(t, anaID) != 100000-2590 || f.balanceOf(t, brunoID) != 50000+2590 {
		t.Error("the money moved twice")
	}
	if page, _ := f.svc.List(context.Background(), ListInput{AccountID: brunoID}); len(page.Payments) != 1 || page.Payments[0].Status != Settled {
		t.Errorf("Bruno's payments: %+v", page.Payments)
	}
}

func TestBookTransferResumeByWorker(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, p := f.stopHalfway(t, 2590)

	if n, err := f.svc.CompleteBookTransfers(ctx); err != nil || n != 0 {
		t.Fatalf("too soon: %d, %v", n, err)
	}
	f.advance(resubmitAfter + time.Second)
	if n, err := f.svc.CompleteBookTransfers(ctx); err != nil || n != 1 {
		t.Fatalf("%d, %v", n, err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Settled || got.FinishedAt.IsZero() {
		t.Errorf("payment = %+v", got)
	}
	if f.balanceOf(t, anaID) != 100000-2590 || f.balanceOf(t, brunoID) != 50000+2590 {
		t.Error("the money moved twice")
	}
	if n, _ := f.svc.CompleteBookTransfers(ctx); n != 0 {
		t.Errorf("finished %d again", n)
	}
}

func TestBookTransferDropped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolveBook(t)
	if _, _, err := f.store.Insert(ctx, Payment{
		AccountID: anaID, Direction: Outbound, Type: TypeBookTransfer, Status: Pending, AmountCents: 200000, EndToEndID: r.EndToEndID,
		ResolutionID: r.ID, Counterparty: r.Payee, CreatedAt: f.clock,
	}); err != nil {
		t.Fatal(err)
	}
	f.advance(resubmitAfter + time.Second)
	if n, err := f.svc.CompleteBookTransfers(ctx); err != nil || n != 0 {
		t.Fatalf("%d, %v", n, err)
	}
	if _, found, _ := f.store.ByResolution(ctx, r.ID); found {
		t.Error("the payment is still there")
	}
	if f.balanceOf(t, anaID) != 100000 || f.balanceOf(t, brunoID) != 50000 {
		t.Error("money moved")
	}
}

func TestReportBookTransfers(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolveBook(t)
	p := f.pay(t, r, 2590)

	if n, err := f.svc.ReportBookTransfers(ctx); err != nil || n != 1 {
		t.Fatalf("reported %d, %v", n, err)
	}
	if f.bank.sent() != 1 {
		t.Fatalf("sent %d messages", f.bank.sent())
	}
	body := f.bank.bodies[0]
	if kind, _ := pix.MessageKind(body); kind != "trck.002" {
		t.Fatalf("kind = %q", kind)
	}
	s := compact(body)
	for _, want := range []string{
		"<EndToEndId>" + r.EndToEndID + "</EndToEndId>",
		`<IntrBkSttlmAmt Ccy="BRL">25.90</IntrBkSttlmAmt>`,
		"<Prtry>BOK1</Prtry>",
		"<Prtry>DICT</Prtry>",
		"<Prxy><Id>" + brunoKey + "</Id></Prxy>",
		"<Dbtr><Pty><Id><PrvtId><Othr><Id>" + anaTaxID + "</Id>",
		"<Cdtr><Pty><Id><PrvtId><Othr><Id>" + brunoTaxID + "</Id>",
		"<MmbId>" + walletISPB + "</MmbId>",
		"<Id>00234567</Id>",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in:\n%s", want, s)
		}
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.ReportStatus != ReportSent || got.ReportSentAt.IsZero() {
		t.Errorf("payment = %+v", got)
	}

	if n, _ := f.svc.ReportBookTransfers(ctx); n != 0 {
		t.Errorf("reported %d again at once", n)
	}
	f.advance(reportRetryAfter + time.Second)
	if n, _ := f.svc.ReportBookTransfers(ctx); n != 1 {
		t.Errorf("reported %d after a minute without a receipt", n)
	}

	if err := f.svc.OnReceipt(ctx, pix.Receipt{Ref: r.EndToEndID, Status: pix.ReceiptAccepted}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.ReportStatus != ReportAccepted {
		t.Errorf("status = %q", got.ReportStatus)
	}
	f.advance(10 * reportRetryAfter)
	if n, _ := f.svc.ReportBookTransfers(ctx); n != 0 {
		t.Errorf("an accepted report was sent again (%d)", n)
	}
}

func TestReportOutboundOnly(t *testing.T) {
	f := newFixture(t)
	f.pay(t, f.resolveBook(t), 100)
	f.pay(t, f.resolve(t), 100)

	if n, err := f.svc.ReportBookTransfers(context.Background()); err != nil || n != 1 {
		t.Fatalf("reported %d, %v", n, err)
	}
}

func TestReportWaitsForSPI(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.pay(t, f.resolveBook(t), 100)

	f.bank.err = fmt.Errorf("%w: connection refused", pix.ErrUnavailable)
	if n, err := f.svc.ReportBookTransfers(ctx); err != nil || n != 0 {
		t.Fatalf("with the SPI down: %d, %v", n, err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.ReportStatus != "" {
		t.Fatalf("status = %q", got.ReportStatus)
	}
	f.bank.err = nil
	if n, _ := f.svc.ReportBookTransfers(ctx); n != 1 {
		t.Errorf("reported %d once the SPI was back", n)
	}
}

func TestReportRefused(t *testing.T) {
	for _, tt := range []struct {
		code   string
		status string
		resent bool
	}{
		{"FF08", ReportRejected, false},
		{"AG01", ReportRejected, false},
		{"ED05", "", true},
	} {
		t.Run(tt.code, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			r := f.resolveBook(t)
			p := f.pay(t, r, 100)
			if n, _ := f.svc.ReportBookTransfers(ctx); n != 1 {
				t.Fatal("not reported")
			}

			err := f.svc.OnReceipt(ctx, pix.Receipt{Ref: r.EndToEndID, Status: pix.ReceiptRejected, ReasonCode: tt.code, Detail: "text of the SPI"})
			if err != nil {
				t.Fatal(err)
			}
			got, _ := f.svc.Get(ctx, p.ID)
			if got.ReportStatus != tt.status || got.ReportReason != tt.code {
				t.Errorf("payment = %+v", got)
			}
			f.advance(reportRetryAfter + time.Second)
			n, _ := f.svc.ReportBookTransfers(ctx)
			if (n == 1) != tt.resent {
				t.Errorf("sent again = %v, want %v", n == 1, tt.resent)
			}
		})
	}
}

func TestReceiptIgnored(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.pay(t, f.resolve(t), 100)

	for name, r := range map[string]pix.Receipt{
		"a payment through the SPI": {Ref: p.EndToEndID, Status: pix.ReceiptAccepted},
		"a return":                  {Ref: "D99000001202611211200abcdefghijk", Status: pix.ReceiptAccepted},
		"nothing we know":           {Ref: "E99000001202611211200zzzzzzzzzzz", Status: pix.ReceiptRejected, ReasonCode: "FF08"},
	} {
		if err := f.svc.OnReceipt(ctx, r); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.ReportStatus != "" {
		t.Errorf("status = %q", got.ReportStatus)
	}
}

func TestHandleCamt025(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolveBook(t)
	p := f.pay(t, r, 100)
	if n, _ := f.svc.ReportBookTransfers(ctx); n != 1 {
		t.Fatal("not reported")
	}

	receipt := fmt.Sprintf(`<Envelope xmlns="https://www.bcb.gov.br/pi/camt.025/1.0"><AppHdr><MsgDefIdr>camt.025.spi.1.0</MsgDefIdr></AppHdr>
	<Document><Rct><MsgHdr><MsgId>M00038166abcdefghijklmnopqrstuvw</MsgId></MsgHdr><RctDtls>
	<OrgnlMsgId><MsgId>M99000001abcdefghijklmnopqrstuvw</MsgId></OrgnlMsgId><OrgnlPmtId><PrtryId>%s</PrtryId></OrgnlPmtId>
	<ReqHdlg><Sts><Cd>ACPT</Cd></Sts></ReqHdlg></RctDtls></Rct></Document></Envelope>`, r.EndToEndID)
	if err := f.svc.Handle(ctx, pix.Message{ResourceID: "r", Body: []byte(receipt)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.ReportStatus != ReportAccepted {
		t.Errorf("status = %q", got.ReportStatus)
	}
}

func TestResendReport(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolveBook(t)
	p := f.pay(t, r, 100)
	if n, _ := f.svc.ReportBookTransfers(ctx); n != 1 {
		t.Fatal("not reported")
	}
	if err := f.svc.OnReceipt(ctx, pix.Receipt{Ref: r.EndToEndID, Status: pix.ReceiptRejected, ReasonCode: "AG01"}); err != nil {
		t.Fatal(err)
	}

	refused, err := f.svc.RejectedReports(ctx)
	if err != nil || len(refused) != 1 || refused[0].ID != p.ID || refused[0].ReportReason != "AG01" {
		t.Fatalf("%+v, %v", refused, err)
	}
	if ok, err := f.svc.ResendReport(ctx, "not-a-uuid"); ok || err != nil {
		t.Fatalf("a bad id: %v, %v", ok, err)
	}
	if ok, err := f.svc.ResendReport(ctx, p.ID); !ok || err != nil {
		t.Fatalf("resend: %v, %v", ok, err)
	}
	if ok, _ := f.svc.ResendReport(ctx, p.ID); ok {
		t.Fatal("a report that is not refused cannot be reset")
	}
	if n, err := f.svc.ReportBookTransfers(ctx); err != nil || n != 1 {
		t.Fatalf("sent %d, %v", n, err)
	}
	if refused, _ := f.svc.RejectedReports(ctx); len(refused) != 0 {
		t.Fatalf("still refused: %+v", refused)
	}
}

func TestBookTransferOver40s(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r, p := f.stopHalfway(t, 400)
	f.advance(bookTransferLimit + time.Second)

	var logged bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })

	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 400, Message: "lunch"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Settled {
		t.Fatalf("status %s: the money may have moved, so it is settled", got.Status)
	}
	if !strings.Contains(logged.String(), "book transfer over 40s") {
		t.Fatalf("not logged: %q", logged.String())
	}
}
