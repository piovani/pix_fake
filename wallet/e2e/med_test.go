package e2e

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// The MED with the wallet as the payee's participant: Banco Alfa, whose customer paid Ana, reports the payment as a
// fraud in the DICT. The worker acknowledges the report and blocks the money; the analysis (cmd/med) agrees or not;
// a refund asked by Banco Alfa is answered with a return FR01 from the block, and closed with its RtrId.

type dictReport struct {
	ID             string `xml:"Id"`
	Status         string `xml:"Status"`
	AnalysisResult string `xml:"AnalysisResult"`
}

type dictRefund struct {
	ID                  string `xml:"Id"`
	Status              string `xml:"Status"`
	AnalysisResult      string `xml:"AnalysisResult"`
	RefundTransactionID string `xml:"RefundTransactionId"`
}

// asBank calls the DICT API as Banco Alfa, the payer's participant, and decodes the element at path of the answer.
func (e env) asBank(t *testing.T, method, path, body string, want int, out any) {
	t.Helper()
	req, _ := http.NewRequest(method, e.control+"/api/v2"+path, strings.NewReader(body))
	req.Header.Set("X-Participant-ISPB", bankAlfa)
	req.Header.Set("PI-RequestingParticipant", bankAlfa)
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d\n%s", method, path, resp.StatusCode, want, raw)
	}
	if out != nil {
		if err := xml.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s %s: %v\n%s", method, path, err, raw)
		}
	}
}

func (e env) report(t *testing.T, id string) dictReport {
	t.Helper()
	var out struct {
		Report dictReport `xml:"InfractionReport"`
	}
	e.asBank(t, http.MethodGet, "/infraction-reports/"+id, "", http.StatusOK, &out)
	return out.Report
}

func (e env) refund(t *testing.T, id string) dictRefund {
	t.Helper()
	var out struct {
		Refund dictRefund `xml:"Refund"`
	}
	e.asBank(t, http.MethodGet, "/refunds/"+id, "", http.StatusOK, &out)
	return out.Refund
}

// eventually waits for cond, checking every 250 ms; the worker acts every 5 seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("%s did not happen", what)
}

// reportPayment has Banco Alfa pay Ana amount and report it as a fraud once the DICT knows it, and waits for the wallet
// to acknowledge it.
func (e env) reportPayment(t *testing.T, amount string) (string, string) {
	t.Helper()
	e2e := e.bankPays(t, `{"from":"99000002","to":"99000001","to_account":"00123456","amount":"`+amount+`","key":"ana.souza@example.com"}`)
	if got := e.waitReceived(t, e2e); got == nil || got.Status.String() != "PAYMENT_STATUS_SETTLED" {
		t.Fatalf("received: %v", got)
	}
	e.waitDICTKnows(t, e2e)
	var out struct {
		Report dictReport `xml:"InfractionReport"`
	}
	e.asBank(t, http.MethodPost, "/infraction-reports", `<CreateInfractionReportRequest><Participant>`+bankAlfa+`</Participant>`+
		`<InfractionReport><TransactionId>`+e2e+`</TransactionId><Reason>REFUND_REQUEST</Reason><SituationType>SCAM</SituationType>`+
		`<ReportDetails>Golpe do falso parente</ReportDetails></InfractionReport></CreateInfractionReportRequest>`, http.StatusCreated, &out)
	id := out.Report.ID
	eventually(t, "the wallet acknowledging report "+id, func() bool { return e.report(t, id).Status == "ACKNOWLEDGED" })
	return e2e, id
}

// waitDICTKnows waits until the SPI has told the DICT of the settled payment.
func (e env) waitDICTKnows(t *testing.T, e2e string) {
	t.Helper()
	eventually(t, "the DICT learning of "+e2e, func() bool {
		resp, err := http.Get(e.control + "/mock/transactions")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var list struct {
			Transactions []struct {
				ID string `json:"end_to_end_id"`
			} `json:"transactions"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&list)
		for _, tx := range list.Transactions {
			if tx.ID == e2e {
				return true
			}
		}
		return false
	})
}

func needMED(t *testing.T) env {
	t.Helper()
	e := setup(t)
	if e.spi == "" || os.Getenv("WALLET_E2E_BIN") == "" {
		t.Skip("WALLET_E2E_SPI_URL and WALLET_E2E_BIN are needed")
	}
	return e
}

func TestMEDRefund(t *testing.T) {
	e := needMED(t)
	before := e.balance(t)

	e2e, report := e.reportPayment(t, "20.00")
	if got := e.balance(t); got.AvailableCents != before.AvailableCents || got.ReservedCents != before.ReservedCents+2000 {
		t.Fatalf("balance %v -> %v; want the 20.00 received blocked", before, got)
	}
	if out := run(t, "med", "list"); !strings.Contains(out, report) || !strings.Contains(out, e2e) {
		t.Fatalf("med list does not show the report:\n%s", out)
	}
	run(t, "med", "agree", report, "SCAMMER_ACCOUNT", "Conta usada em golpes")
	if r := e.report(t, report); r.Status != "CLOSED" || r.AnalysisResult != "AGREED" {
		t.Fatalf("report after the analysis = %+v", r)
	}

	var created struct {
		Refund dictRefund `xml:"Refund"`
	}
	e.asBank(t, http.MethodPost, "/refunds", `<CreateRefundRequest><Participant>`+bankAlfa+`</Participant><Refund><TransactionId>`+e2e+
		`</TransactionId><RefundReason>FRAUD</RefundReason><RefundAmount>20.00</RefundAmount></Refund></CreateRefundRequest>`, http.StatusCreated, &created)
	var refund dictRefund
	eventually(t, "the wallet closing the refund", func() bool {
		refund = e.refund(t, created.Refund.ID)
		return refund.Status == "CLOSED"
	})
	if refund.AnalysisResult != "TOTALLY_ACCEPTED" || !strings.HasPrefix(refund.RefundTransactionID, "D"+walletISPB) {
		t.Fatalf("refund = %+v", refund)
	}
	ret := e.mockPayment(t, refund.RefundTransactionID, func(p map[string]any) bool { return p["status"] == "SETTLED" })
	if ret["return_reason"] != "FR01" || ret["end_to_end_id"] != e2e || ret["amount"] != "20.00" {
		t.Fatalf("the return in the SPI = %v", ret)
	}
	if got := e.balance(t); got.AvailableCents != before.AvailableCents || got.ReservedCents != before.ReservedCents {
		t.Fatalf("balance %v -> %v; want the 20.00 gone back", before, got)
	}
}

func TestMEDDisagree(t *testing.T) {
	e := needMED(t)
	before := e.balance(t)
	_, report := e.reportPayment(t, "7.00")
	if got := e.balance(t); got.ReservedCents != before.ReservedCents+700 {
		t.Fatalf("balance %v -> %v; want 7.00 blocked", before, got)
	}
	if out, code := runExit(t, "med", "agree", report, "NONSENSE"); code == 0 {
		t.Fatalf("agree with a bad fraud type succeeded:\n%s", out)
	}
	run(t, "med", "disagree", report, "Pagamento legitimo")
	if r := e.report(t, report); r.Status != "CLOSED" || r.AnalysisResult != "DISAGREED" {
		t.Fatalf("report = %+v", r)
	}
	if got := e.balance(t); got.AvailableCents != before.AvailableCents+700 || got.ReservedCents != before.ReservedCents {
		t.Fatalf("balance %v -> %v; want the block given back", before, got)
	}
	if out, code := runExit(t, "med", "disagree", report); code == 0 {
		t.Fatalf("analysing twice succeeded:\n%s", out)
	}
}

func TestMEDCancelledReport(t *testing.T) {
	e := needMED(t)
	before := e.balance(t)
	_, report := e.reportPayment(t, "3.00")
	e.asBank(t, http.MethodPost, "/infraction-reports/"+report+"/cancel", `<CancelInfractionReportRequest><InfractionReportId>`+report+
		`</InfractionReportId><Participant>`+bankAlfa+`</Participant></CancelInfractionReportRequest>`, http.StatusOK, nil)
	eventually(t, "the block given back", func() bool {
		got := e.balance(t)
		return got.AvailableCents == before.AvailableCents+300 && got.ReservedCents == before.ReservedCents
	})
}
