package pix

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests need the running mocks (`make setup`) and WALLET_TEST_SPI_URL, such as http://localhost:8081/api/v1.
func liveSPI(t *testing.T) *SPIClient {
	t.Helper()
	base := os.Getenv("WALLET_TEST_SPI_URL")
	if base == "" {
		t.Skip("WALLET_TEST_SPI_URL is not set")
	}
	return NewSPIClient(base, "99000001")
}

// readUntil pulls the stream until found returns true for a message, then acknowledges.
func readUntil(t *testing.T, c *SPIClient, found func(kind string, body []byte) bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	next := ""
	for ctx.Err() == nil {
		msgs, n, err := c.Pull(ctx, next)
		if err != nil {
			t.Fatalf("pull: %v", err)
		}
		next = n
		for _, m := range msgs {
			kind, err := MessageKind(m.Body)
			if err == nil && found(kind, m.Body) {
				if err := c.Ack(ctx, next); err != nil {
					t.Fatalf("ack: %v", err)
				}
				return
			}
		}
	}
	t.Fatal("the expected message did not arrive")
}

func TestLivePayAKeyOfAnotherBank(t *testing.T) {
	spi := liveSPI(t)
	dict := liveClient(t)
	ctx := context.Background()

	e2e, _ := NewEndToEndID("99000001", time.Now())
	entry, err := dict.GetEntry(ctx, GetEntryParams{Key: "39053344705", PayerID: "52998224725", EndToEndID: e2e})
	if err != nil {
		t.Fatal(err)
	}

	_, body, err := NewPacs008("99000001", PaymentOrder{
		EndToEndID:  e2e,
		AmountCents: 2590,
		Payer:       Party{ISPB: "99000001", Name: "Ana Souza", TaxID: "52998224725", Branch: "0001", Account: "00123456", AccountType: AccountChecking},
		Receiver:    Party{ISPB: entry.Account.Participant, TaxID: entry.Owner.TaxIDNumber, Branch: entry.Account.Branch, Account: entry.Account.AccountNumber, AccountType: entry.Account.AccountType},
		Key:         entry.Key,
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := spi.Send(ctx, body); err != nil || len(ids) != 1 {
		t.Fatalf("send: %v, %v", ids, err)
	}

	readUntil(t, spi, func(kind string, b []byte) bool {
		if kind != KindPacs002 {
			return false
		}
		sts, _ := ParsePacs002(b)
		for _, st := range sts {
			if st.EndToEndID == e2e {
				if st.Status != StatusSettled {
					t.Errorf("status = %+v, want ACSC", st)
				}
				return true
			}
		}
		return false
	})
}

func TestLiveReceiveAPayment(t *testing.T) {
	spi := liveSPI(t)
	base := strings.Replace(os.Getenv("WALLET_TEST_SPI_URL"), "/api/v1", "", 1)
	ctx := context.Background()

	// the control API makes Banco Alfa pay
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/mock/inbound/payments", strings.NewReader(
		`{"from":"99000002","to":"99000001","to_account":"00123456","amount":"10.50","key":"ana.souza@example.com"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("inbound payment: %v, %v", resp, err)
	}
	resp.Body.Close()

	var received PaymentOrder
	readUntil(t, spi, func(kind string, b []byte) bool {
		if kind != KindPacs008 {
			return false
		}
		orders, err := ParsePacs008(b)
		if err != nil || len(orders) == 0 || orders[0].Receiver.Account != "00123456" || orders[0].AmountCents != 1050 {
			return false
		}
		received = orders[0]
		return true
	})
	if received.Payer.ISPB != "99000002" || received.Key != "ana.souza@example.com" {
		t.Fatalf("received: %+v", received)
	}

	_, answer, err := NewPacs002("99000001", TxStatus{EndToEndID: received.EndToEndID, Status: StatusAccepted}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spi.Send(ctx, answer); err != nil {
		t.Fatalf("send ACSP: %v", err)
	}

	readUntil(t, spi, func(kind string, b []byte) bool {
		sts, _ := ParsePacs002(b)
		for _, st := range sts {
			if kind == KindPacs002 && st.EndToEndID == received.EndToEndID {
				if st.Status != StatusCredit {
					t.Errorf("status = %+v, want ACCC", st)
				}
				return true
			}
		}
		return false
	})
}

// receiveAPayment makes Banco Alfa pay the wallet through the control API and plays the receiving side up to the
// credit (ACSP sent, ACCC received).
func receiveAPayment(t *testing.T, spi *SPIClient, amount string) PaymentOrder {
	t.Helper()
	base := strings.Replace(os.Getenv("WALLET_TEST_SPI_URL"), "/api/v1", "", 1)
	ctx := context.Background()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/mock/inbound/payments", strings.NewReader(
		`{"from":"99000002","to":"99000001","to_account":"00123456","amount":"`+amount+`","key":"ana.souza@example.com"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode >= 300 {
		t.Fatalf("inbound payment: %v, %v", resp, err)
	}
	resp.Body.Close()

	var received PaymentOrder
	readUntil(t, spi, func(kind string, b []byte) bool {
		if kind != KindPacs008 {
			return false
		}
		orders, err := ParsePacs008(b)
		if err != nil || len(orders) == 0 || orders[0].Receiver.Account != "00123456" || orders[0].AmountCents != amountCents(t, amount) {
			return false
		}
		received = orders[0]
		return true
	})
	_, answer, err := NewPacs002("99000001", TxStatus{EndToEndID: received.EndToEndID, Status: StatusAccepted}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := spi.Send(ctx, answer); err != nil {
		t.Fatalf("send ACSP: %v", err)
	}
	readUntil(t, spi, func(kind string, b []byte) bool {
		sts, _ := ParsePacs002(b)
		return kind == KindPacs002 && len(sts) > 0 && sts[0].EndToEndID == received.EndToEndID && sts[0].Status == StatusCredit
	})
	return received
}

func amountCents(t *testing.T, s string) int64 {
	t.Helper()
	c, err := parseAmount(s)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLiveReturnAReceivedPayment(t *testing.T) {
	spi := liveSPI(t)
	ctx := context.Background()
	received := receiveAPayment(t, spi, "12.34")

	rtrID, err := NewReturnID("99000001", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, body, err := NewPacs004("99000001", PaymentReturn{
		ReturnID: rtrID, EndToEndID: received.EndToEndID, AmountCents: received.AmountCents, To: received.Payer.ISPB, Detail: "customer asked",
	}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := spi.Send(ctx, body); err != nil || len(ids) != 1 {
		t.Fatalf("send return: %v, %v", ids, err)
	}

	// the SPI settles it and tells the sender, naming the return by its RtrId
	readUntil(t, spi, func(kind string, b []byte) bool {
		sts, _ := ParsePacs002(b)
		for _, st := range sts {
			if kind == KindPacs002 && st.InstrID == rtrID {
				if st.Status != StatusSettled || st.EndToEndID != received.EndToEndID {
					t.Errorf("status = %+v, want ACSC", st)
				}
				return true
			}
		}
		return false
	})
}

func TestLiveReportABookTransfer(t *testing.T) {
	spi := liveSPI(t)
	ctx := context.Background()

	e2e, err := NewEndToEndID("99000001", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	msgID, body, err := NewTrck002("99000001", []ReportedTx{{
		EndToEndID:  e2e,
		AmountCents: 500,
		SettledAt:   time.Now().Add(-time.Second),
		Payer:       Party{ISPB: "99000001", TaxID: "52998224725", Branch: "0001", Account: "00123456", AccountType: AccountChecking},
		Receiver:    Party{ISPB: "99000001", TaxID: "39053344705", Branch: "0001", Account: "00456789", AccountType: AccountChecking},
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if ids, err := spi.Send(ctx, body); err != nil || len(ids) != 1 {
		t.Fatalf("send report: %v, %v", ids, err)
	}

	readUntil(t, spi, func(kind string, b []byte) bool {
		if kind != KindCamt025 {
			return false
		}
		rs, err := ParseCamt025(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			if r.ReportMsgID == msgID {
				if r.Rejected() || r.Ref != e2e {
					t.Errorf("receipt = %+v, want ACPT for %s", r, e2e)
				}
				return true
			}
		}
		return false
	})
}
