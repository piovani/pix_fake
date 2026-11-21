package e2e

import (
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

func (e env) balance(t *testing.T) *walletv1.GetBalanceResponse {
	t.Helper()
	res, err := e.accounts.GetBalance(ctx(t), &walletv1.GetBalanceRequest{AccountId: seed.AnaID})
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

	// repeating the call is safe: the same payment, no second charge
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

	// a key of the wallet itself is a book transfer, which is not built yet
	_, err = e.payments.ResolveKey(ctx(t), &walletv1.ResolveKeyRequest{AccountId: seed.AnaID, Key: brunoCPF})
	wantCode(t, "a key of the wallet", err, codes.Unimplemented)

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

// The SPI being down does not fail the payment: it is created, the money is held, and the worker sends it when the SPI
// is back.
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
