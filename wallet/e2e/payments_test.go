package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/seed"
)

// Keys of the DICT seed. Carlos is a customer of Banco Alfa who accepts every payment; the others hold the SPI mock's test
// accounts, which refuse or ignore a payment in a fixed way (see the SPI mock README).
const (
	carlosPhone   = "+5521999990003" // accepts
	eduardoPhone  = "+5521999990005" // closed account: AC07
	fernandaPhone = "+5521999990006" // blocked account: AC06
	gustavoPhone  = "+5531999990007" // never answers: the SPI times out with AB03
	helenaCPF     = "10293847541"    // DS04
	igorCPF       = "56473829164"    // FRAD
	larissaCPF    = "64528193728"    // ED05
	marcosCPF     = "82910476340"    // AB09
)

func (e env) balance(t *testing.T) *walletv1.GetBalanceResponse { return e.balanceOf(t, seed.AnaID) }

func (e env) balanceOf(t *testing.T, account string) *walletv1.GetBalanceResponse {
	t.Helper()
	res, err := e.accounts.GetBalance(ctx(t), &walletv1.GetBalanceRequest{AccountId: account})
	if err != nil {
		t.Fatalf("GetBalance: %v", err)
	}
	return res
}

func (e env) resolve(t *testing.T, key string) *walletv1.ResolveKeyResponse {
	t.Helper()
	res, err := e.payments.ResolveKey(ctx(t), &walletv1.ResolveKeyRequest{AccountId: seed.AnaID, Key: key})
	if err != nil {
		t.Fatalf("ResolveKey(%s): %v", key, err)
	}
	return res
}

func (e env) pay(t *testing.T, resolution string, cents int64, message string) (*walletv1.Payment, error) {
	t.Helper()
	res, err := e.payments.CreatePayment(ctx(t), &walletv1.CreatePaymentRequest{ResolutionId: resolution, AmountCents: cents, Message: message})
	return res.GetPayment(), err
}

// waitFinished polls the payment until the worker has applied the SPI's answer. The SPI times a silent payment out after
// 2 seconds in `make e2e`, and after 40 against the environment of `make up`.
func (e env) waitFinished(t *testing.T, id string, within time.Duration) *walletv1.Payment {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		res, err := e.payments.GetPayment(ctx(t), &walletv1.GetPaymentRequest{PaymentId: id})
		if err != nil {
			t.Fatalf("GetPayment: %v", err)
		}
		if res.Payment.Status != walletv1.PaymentStatus_PAYMENT_STATUS_PENDING {
			return res.Payment
		}
		if time.Now().After(deadline) {
			t.Fatalf("payment %s still pending after %s: is the worker running? %v", id, within, res.Payment)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestPayAKeyOfAnotherBank(t *testing.T) {
	e := setup(t)
	before := e.balance(t)

	r := e.resolve(t, carlosPhone)
	if r.BookTransfer || r.ResolutionId == "" || r.Payee.Name != "Carlos Mendes" || r.Payee.Participant != "99000002" ||
		r.Payee.AccountNumber != "00456789" || r.Payee.TaxId != "39053344705" {
		t.Fatalf("resolution: %v", r)
	}
	if e.balance(t).AvailableCents != before.AvailableCents {
		t.Fatal("resolving a key must not move money")
	}

	p, err := e.pay(t, r.ResolutionId, 150, "lunch")
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if p.Status != walletv1.PaymentStatus_PAYMENT_STATUS_PENDING || p.Direction != walletv1.PaymentDirection_PAYMENT_DIRECTION_OUTBOUND ||
		p.Type != walletv1.PaymentType_PAYMENT_TYPE_PIX || p.AmountCents != 150 || p.Message != "lunch" || p.Counterparty.Name != "Carlos Mendes" ||
		!strings.HasPrefix(p.EndToEndId, "E99000001") || len(p.EndToEndId) != 32 {
		t.Fatalf("payment: %v", p)
	}

	done := e.waitFinished(t, p.Id, 60*time.Second)
	if done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED || done.FinishedAt == nil || done.RejectCode != "" {
		t.Fatalf("finished: %v", done)
	}
	after := e.balance(t)
	if after.AvailableCents != before.AvailableCents-150 || after.ReservedCents != before.ReservedCents {
		t.Errorf("balance %v -> %v, want 150 less and nothing reserved", before, after)
	}

	again, err := e.pay(t, r.ResolutionId, 150, "lunch")
	if err != nil || again.Id != p.Id || again.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("repeat: %v, %v", again, err)
	}
	if e.balance(t).AvailableCents != after.AvailableCents {
		t.Error("a repeated call charged again")
	}
	_, err = e.pay(t, r.ResolutionId, 999, "lunch")
	wantCode(t, "another amount", err, codes.AlreadyExists)

	list, err := e.payments.ListPayments(ctx(t), &walletv1.ListPaymentsRequest{AccountId: seed.AnaID, PageSize: 5})
	if err != nil || len(list.Payments) == 0 || list.Payments[0].Id != p.Id {
		t.Errorf("the newest payment must come first: %v, %v", list, err)
	}
}

func TestRejectedPaymentsGiveTheMoneyBack(t *testing.T) {
	e := setup(t)
	tests := []struct {
		name, key, code string
		within          time.Duration
	}{
		{"closed account", eduardoPhone, "AC07", 30 * time.Second},
		{"blocked account", fernandaPhone, "AC06", 30 * time.Second},
		{"the bank refuses", helenaCPF, "DS04", 30 * time.Second},
		{"suspected fraud", igorCPF, "FRAD", 30 * time.Second},
		{"processing error", larissaCPF, "ED05", 30 * time.Second},
		{"error at the bank", marcosCPF, "AB09", 30 * time.Second},
		{"the bank never answers", gustavoPhone, "AB03", 70 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			before := e.balance(t)
			r := e.resolve(t, tt.key)
			p, err := e.pay(t, r.ResolutionId, 100, "")
			if err != nil {
				t.Fatalf("CreatePayment: %v", err)
			}
			if got := e.balance(t); got.ReservedCents != before.ReservedCents+100 {
				t.Errorf("the money must be held while the SPI decides: %v -> %v", before, got)
			}

			done := e.waitFinished(t, p.Id, tt.within)
			if done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_REJECTED || done.RejectCode != tt.code || done.RejectReason == "" || done.FinishedAt == nil {
				t.Fatalf("finished: %v", done)
			}
			if tt.code == "FRAD" && strings.Contains(strings.ToLower(done.RejectReason), "fraud") {
				t.Errorf("the customer must not be told about fraud: %q", done.RejectReason)
			}
			if after := e.balance(t); after.AvailableCents != before.AvailableCents || after.ReservedCents != before.ReservedCents {
				t.Errorf("the money must be back: %v -> %v", before, after)
			}
		})
	}
}

func TestPayErrors(t *testing.T) {
	e := setup(t)
	before := e.balance(t)

	_, err := e.payments.ResolveKey(ctx(t), &walletv1.ResolveKeyRequest{AccountId: seed.AnaID, Key: "nobody@example.com"})
	wantCode(t, "unknown key", err, codes.NotFound)

	_, err = e.payments.ResolveKey(ctx(t), &walletv1.ResolveKeyRequest{AccountId: seed.AnaID, Key: anaCPF})
	wantCode(t, "the payer's own key", err, codes.InvalidArgument)

	r := e.resolve(t, carlosPhone)
	_, err = e.pay(t, r.ResolutionId, before.AvailableCents+1, "")
	wantCode(t, "insufficient funds", err, codes.FailedPrecondition)
	if got := e.balance(t); got.AvailableCents != before.AvailableCents || got.ReservedCents != before.ReservedCents {
		t.Errorf("a refused payment moved money: %v -> %v", before, got)
	}
	_, err = e.pay(t, r.ResolutionId, 0, "")
	wantCode(t, "zero amount", err, codes.InvalidArgument)
	_, err = e.pay(t, "0a1b2c3d-9999-4000-8000-000000000009", 100, "")
	wantCode(t, "unknown resolution", err, codes.NotFound)
	_, err = e.payments.GetPayment(ctx(t), &walletv1.GetPaymentRequest{PaymentId: "0a1b2c3d-9999-4000-8000-000000000009"})
	wantCode(t, "unknown payment", err, codes.NotFound)
	_, err = e.payments.ResolveKey(ctx(t), &walletv1.ResolveKeyRequest{AccountId: "0a1b2c3d-9999-4000-8000-000000000009", Key: carlosPhone})
	wantCode(t, "unknown account", err, codes.NotFound)
}

func TestPaymentMadeWhileTheSPIIsDownIsSentByTheWorker(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	e.clearSPIFaults(t)
	t.Cleanup(func() { e.clearSPIFaults(t) })
	before := e.balance(t)

	r := e.resolve(t, carlosPhone)
	e.failSPI(t, http.MethodPost, "/api/v1/in/", 503, 1)
	p, err := e.pay(t, r.ResolutionId, 120, "while down")
	if err != nil {
		t.Fatalf("CreatePayment must not fail when the SPI is down: %v", err)
	}
	if p.Status != walletv1.PaymentStatus_PAYMENT_STATUS_PENDING {
		t.Fatalf("payment: %v", p)
	}

	done := e.waitFinished(t, p.Id, 60*time.Second)
	if done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("finished: %v", done)
	}
	if after := e.balance(t); after.AvailableCents != before.AvailableCents-120 || after.ReservedCents != before.ReservedCents {
		t.Errorf("balance %v -> %v", before, after)
	}
}

func (e env) failSPI(t *testing.T, method, pathPrefix string, statusCode, count int) {
	t.Helper()
	body := fmt.Sprintf(`{"method":%q,"path_prefix":%q,"status":%d,"retry_after":1,"count":%d}`, method, pathPrefix, statusCode, count)
	resp, err := http.Post(e.spi+"/mock/faults", "application/json", strings.NewReader(body))
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("could not add the SPI fault: %v, %v", resp, err)
	}
	resp.Body.Close()
}

func (e env) clearSPIFaults(t *testing.T) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, e.spi+"/mock/faults", nil)
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
	}
}

func TestPayAnotherCustomerOfTheWallet(t *testing.T) {
	e := setup(t)
	ana, bruno := e.balance(t), e.balanceOf(t, seed.BrunoID)

	r := e.resolve(t, "+5511999990002")
	if !r.BookTransfer || r.Payee.Name != "Bruno Lima" || r.Payee.Participant != "99000001" || r.Payee.AccountNumber != "00234567" {
		t.Fatalf("resolution: %v", r)
	}

	p, err := e.pay(t, r.ResolutionId, 175, "to Bruno")
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	if p.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED || p.Type != walletv1.PaymentType_PAYMENT_TYPE_BOOK_TRANSFER ||
		p.Direction != walletv1.PaymentDirection_PAYMENT_DIRECTION_OUTBOUND || p.FinishedAt == nil || p.Counterparty.Name != "Bruno Lima" {
		t.Fatalf("a book transfer is settled in the same call: %v", p)
	}
	if got := e.balance(t); got.AvailableCents != ana.AvailableCents-175 || got.ReservedCents != ana.ReservedCents {
		t.Errorf("Ana: %v -> %v", ana, got)
	}
	if got := e.balanceOf(t, seed.BrunoID); got.AvailableCents != bruno.AvailableCents+175 {
		t.Errorf("Bruno: %v -> %v", bruno, got)
	}

	list, err := e.payments.ListPayments(ctx(t), &walletv1.ListPaymentsRequest{AccountId: seed.BrunoID, PageSize: 5})
	if err != nil || len(list.Payments) == 0 {
		t.Fatalf("Bruno's payments: %v, %v", list, err)
	}
	in := list.Payments[0]
	if in.Direction != walletv1.PaymentDirection_PAYMENT_DIRECTION_INBOUND || in.Type != walletv1.PaymentType_PAYMENT_TYPE_BOOK_TRANSFER ||
		in.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED || in.AmountCents != 175 || in.EndToEndId != p.EndToEndId ||
		in.Counterparty.Name != "Ana Souza" || in.Message != "to Bruno" {
		t.Errorf("what Bruno received: %v", in)
	}

	again, err := e.pay(t, r.ResolutionId, 175, "to Bruno")
	if err != nil || again.Id != p.Id || e.balance(t).AvailableCents != ana.AvailableCents-175 {
		t.Errorf("repeat: %v, %v", again, err)
	}

	if e.spi == "" {
		return
	}
	if status := e.spiStatus(t, "/mock/payments/"+p.EndToEndId); status != http.StatusNotFound {
		t.Errorf("the SPI has a payment for a book transfer (status %d)", status)
	}
	deadline := time.Now().Add(30 * time.Second)
	for e.spiStatus(t, "/mock/reports/"+p.EndToEndId) != http.StatusOK {
		if time.Now().After(deadline) {
			t.Fatal("the SPI was not told about the book transfer: is the worker running?")
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (e env) spiStatus(t *testing.T, path string) int {
	t.Helper()
	resp, err := http.Get(e.spi + path)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestPayAnotherCustomerWithoutEnoughMoney(t *testing.T) {
	e := setup(t)
	ana, bruno := e.balance(t), e.balanceOf(t, seed.BrunoID)

	r := e.resolve(t, "+5511999990002")
	_, err := e.pay(t, r.ResolutionId, ana.AvailableCents+1, "")
	wantCode(t, "insufficient funds", err, codes.FailedPrecondition)
	if got := e.balance(t); got.AvailableCents != ana.AvailableCents || e.balanceOf(t, seed.BrunoID).AvailableCents != bruno.AvailableCents {
		t.Errorf("a refused payment moved money: Ana %v -> %v", ana, got)
	}
	if p, err := e.pay(t, r.ResolutionId, 1, ""); err != nil || p.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Errorf("after the refusal: %v, %v", p, err)
	}
}

func (e env) bankPays(t *testing.T, body string) string {
	t.Helper()
	resp, err := http.Post(e.spi+"/mock/inbound/payments", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		ID string `json:"id"`
	}
	if resp.StatusCode != http.StatusCreated || json.NewDecoder(resp.Body).Decode(&out) != nil || out.ID == "" {
		t.Fatalf("the simulated bank could not pay: status %d", resp.StatusCode)
	}
	return out.ID
}

func (e env) mockPayment(t *testing.T, id string, done func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := http.Get(e.spi + "/mock/payments/" + id)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&p)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK && done(p) {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatalf("the SPI payment %s never got there: %v", id, p)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func (e env) receivedBy(t *testing.T, account, endToEndID string) *walletv1.Payment {
	t.Helper()
	list, err := e.payments.ListPayments(ctx(t), &walletv1.ListPaymentsRequest{AccountId: account, PageSize: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list.Payments {
		if p.EndToEndId == endToEndID && p.Direction == walletv1.PaymentDirection_PAYMENT_DIRECTION_INBOUND {
			return p
		}
	}
	return nil
}

func (e env) waitReceived(t *testing.T, endToEndID string) *walletv1.Payment {
	t.Helper()
	var got *walletv1.Payment
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if got = e.receivedBy(t, seed.AnaID, endToEndID); got != nil && got.Status != walletv1.PaymentStatus_PAYMENT_STATUS_PENDING {
			break
		}
	}
	return got
}

func TestReceiveAPaymentFromAnotherBank(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	before := e.balance(t)

	id := e.bankPays(t, `{"from":"99000002","to":"99000001","to_account":"00123456","amount":"10.50","key":"ana.souza@example.com"}`)
	e.mockPayment(t, id, func(p map[string]any) bool { return p["settled_at"] != nil })

	got := e.waitReceived(t, id)
	if got == nil || got.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED || got.Type != walletv1.PaymentType_PAYMENT_TYPE_PIX ||
		got.AmountCents != 1050 || got.Counterparty.Participant != "99000002" || got.FinishedAt == nil {
		t.Fatalf("received payment: %v", got)
	}
	if after := e.balance(t); after.AvailableCents != before.AvailableCents+1050 || after.ReservedCents != before.ReservedCents {
		t.Errorf("balance %v -> %v", before, after)
	}
}

func TestPaymentsTheWalletRefusesToReceive(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	tests := []struct{ name, body, code string }{
		{"no such account", `"to_account":"00999999","to_tax_id":"52998224725"`, "AC03"},
		{"another tax id", `"to_account":"00123456","to_tax_id":"39053344705"`, "BE01"},
		{"another account type", `"to_account":"00123456","to_type":"SVGS"`, "AC14"},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			before := e.balance(t)
			id := e.bankPays(t, `{"from":"99000002","to":"99000001","amount":"3.00",`+tt.body+`}`)

			p := e.mockPayment(t, id, func(p map[string]any) bool { return p["reason_code"] != nil })
			if p["reason_code"] != tt.code {
				t.Errorf("the SPI says %v, want %s", p["reason_code"], tt.code)
			}
			if e.receivedBy(t, seed.AnaID, id) != nil || e.balance(t).AvailableCents != before.AvailableCents {
				t.Error("a refused payment left a trace")
			}
		})
	}
}

func TestReturnAReceivedPayment(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	before := e.balance(t)
	id := e.bankPays(t, `{"from":"99000002","to":"99000001","to_account":"00123456","amount":"10.50","key":"ana.souza@example.com"}`)
	received := e.waitReceived(t, id)
	if received == nil || received.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("received: %v", received)
	}

	giveBack := func(cents int64, request string) (*walletv1.Payment, error) {
		res, err := e.payments.ReturnPayment(ctx(t), &walletv1.ReturnPaymentRequest{PaymentId: received.Id, AmountCents: cents, RequestId: request})
		return res.GetPayment(), err
	}
	request := requestID(t)
	first, err := giveBack(400, request)
	if err != nil {
		t.Fatalf("ReturnPayment: %v", err)
	}
	if first.Type != walletv1.PaymentType_PAYMENT_TYPE_RETURN || first.OriginalPaymentId != received.Id || first.AmountCents != 400 ||
		first.Direction != walletv1.PaymentDirection_PAYMENT_DIRECTION_OUTBOUND {
		t.Fatalf("return: %v", first)
	}
	if done := e.waitFinished(t, first.Id, 30*time.Second); done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("finished: %v", done)
	}
	if got := e.balance(t); got.AvailableCents != before.AvailableCents+1050-400 || got.ReservedCents != before.ReservedCents {
		t.Errorf("balance %v -> %v", before, got)
	}
	if res, err := e.payments.GetPayment(ctx(t), &walletv1.GetPaymentRequest{PaymentId: received.Id}); err != nil || res.Payment.ReturnedCents != 400 {
		t.Errorf("original: %v, %v", res, err)
	}

	again, err := giveBack(400, request)
	if err != nil || again.Id != first.Id {
		t.Errorf("repeat: %v, %v", again, err)
	}
	_, err = giveBack(401, request)
	wantCode(t, "another amount", err, codes.AlreadyExists)

	_, err = giveBack(651, requestID(t))
	wantCode(t, "more than what is left", err, codes.FailedPrecondition)
	rest, err := giveBack(650, requestID(t))
	if err != nil {
		t.Fatalf("the rest: %v", err)
	}
	if done := e.waitFinished(t, rest.Id, 30*time.Second); done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("finished: %v", done)
	}
	if got := e.balance(t); got.AvailableCents != before.AvailableCents || got.ReservedCents != before.ReservedCents {
		t.Errorf("everything returned, balance %v -> %v", before, got)
	}
	_, err = giveBack(1, requestID(t))
	wantCode(t, "after returning everything", err, codes.FailedPrecondition)
}

func TestPaymentsThatCannotBeReturned(t *testing.T) {
	e := setup(t)
	r := e.resolve(t, "+5511999990002")
	sent, err := e.pay(t, r.ResolutionId, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.payments.ReturnPayment(ctx(t), &walletv1.ReturnPaymentRequest{PaymentId: sent.Id, AmountCents: 10, RequestId: requestID(t)})
	wantCode(t, "a payment sent", err, codes.FailedPrecondition)
	_, err = e.payments.ReturnPayment(ctx(t), &walletv1.ReturnPaymentRequest{PaymentId: "0a1b2c3d-9999-4000-8000-000000000009", AmountCents: 10, RequestId: requestID(t)})
	wantCode(t, "unknown payment", err, codes.NotFound)
	_, err = e.payments.ReturnPayment(ctx(t), &walletv1.ReturnPaymentRequest{PaymentId: sent.Id, AmountCents: 10, RequestId: "req"})
	wantCode(t, "bad request id", err, codes.InvalidArgument)
}

func TestStatement(t *testing.T) {
	e := setup(t)
	r := e.resolve(t, "+5511999990002")
	p, err := e.pay(t, r.ResolutionId, 175, "statement")
	if err != nil {
		t.Fatal(err)
	}

	statement := func(account string, size int32, token string) *walletv1.GetStatementResponse {
		res, err := e.accounts.GetStatement(ctx(t), &walletv1.GetStatementRequest{AccountId: account, PageSize: size, PageToken: token})
		if err != nil {
			t.Fatalf("GetStatement: %v", err)
		}
		return res
	}

	first := statement(seed.AnaID, 1, "")
	if len(first.Entries) != 1 || first.NextPageToken == "" {
		t.Fatalf("first page: %v", first)
	}
	out := first.Entries[0]
	if out.Type != walletv1.EntryType_ENTRY_TYPE_DEBIT || out.AmountCents != 175 || out.PaymentId != p.Id || out.CounterpartyName != "Bruno Lima" || out.CreatedAt == nil {
		t.Errorf("Ana's entry: %v", out)
	}
	second := statement(seed.AnaID, 1, first.NextPageToken)
	if len(second.Entries) != 1 || second.Entries[0].Id == out.Id {
		t.Errorf("second page: %v", second)
	}

	in := statement(seed.BrunoID, 1, "").Entries[0]
	if in.Type != walletv1.EntryType_ENTRY_TYPE_CREDIT || in.AmountCents != 175 || in.CounterpartyName != "Ana Souza" || in.PaymentId == "" || in.PaymentId == p.Id {
		t.Errorf("Bruno's entry: %v", in)
	}

	_, err = e.accounts.GetStatement(ctx(t), &walletv1.GetStatementRequest{AccountId: "0a1b2c3d-9999-4000-8000-000000000009"})
	wantCode(t, "unknown account", err, codes.NotFound)
	_, err = e.accounts.GetStatement(ctx(t), &walletv1.GetStatementRequest{AccountId: seed.AnaID, PageToken: "garbage"})
	wantCode(t, "bad token", err, codes.InvalidArgument)
}

func TestAnotherBankReturnsAPaymentTheWalletMade(t *testing.T) {
	e := setup(t)
	if e.spi == "" {
		t.Skip("WALLET_E2E_SPI_URL is not set")
	}
	before := e.balance(t)
	r := e.resolve(t, carlosPhone)
	p, err := e.pay(t, r.ResolutionId, 700, "lunch")
	if err != nil {
		t.Fatal(err)
	}
	if done := e.waitFinished(t, p.Id, 60*time.Second); done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("finished: %v", done)
	}

	body := strings.NewReader(fmt.Sprintf(`{"end_to_end_id":%q,"amount":"3.00"}`, p.EndToEndId))
	resp, err := http.Post(e.spi+"/mock/inbound/returns", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("the simulated bank could not return: status %d", resp.StatusCode)
	}

	deadline := time.Now().Add(30 * time.Second)
	for {
		res, err := e.payments.GetPayment(ctx(t), &walletv1.GetPaymentRequest{PaymentId: p.Id})
		if err != nil {
			t.Fatal(err)
		}
		if res.Payment.ReturnedCents == 300 && e.balance(t).AvailableCents == before.AvailableCents-700+300 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the return never arrived: %v, balance %v -> %v", res.Payment, before, e.balance(t))
		}
		time.Sleep(250 * time.Millisecond)
	}

	list, err := e.payments.ListPayments(ctx(t), &walletv1.ListPaymentsRequest{AccountId: seed.AnaID, PageSize: 5})
	if err != nil || len(list.Payments) == 0 {
		t.Fatalf("%v, %v", list, err)
	}
	got := list.Payments[0]
	if got.Type != walletv1.PaymentType_PAYMENT_TYPE_RETURN || got.Direction != walletv1.PaymentDirection_PAYMENT_DIRECTION_INBOUND ||
		got.OriginalPaymentId != p.Id || got.AmountCents != 300 || got.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("the newest payment is not the return: %v", got)
	}
}
