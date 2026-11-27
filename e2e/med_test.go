package e2e

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// dictCall sends a request of the DICT API as participant and returns the status and the body.
func (e *env) dictCall(participant, method, path, body string) (int, []byte) {
	e.t.Helper()
	req, _ := http.NewRequest(method, e.dictURL+"/api/v2"+path, strings.NewReader(body))
	req.Header.Set("X-Participant-ISPB", participant)
	req.Header.Set("PI-RequestingParticipant", participant)
	if body != "" {
		req.Header.Set("Content-Type", "application/xml")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

// dictOK is dictCall that fails the test unless the answer has the status want, and decodes it into v.
func (e *env) dictOK(participant, method, path, body string, want int, v any) {
	e.t.Helper()
	code, out := e.dictCall(participant, method, path, body)
	if code != want {
		e.t.Fatalf("%s %s = %d, want %d\n%s", method, path, code, want, out)
	}
	if v != nil {
		if err := xml.Unmarshal(out, v); err != nil {
			e.t.Fatalf("%s %s: %v\n%s", method, path, err, out)
		}
	}
}

// waitForTransaction waits until the SPI has told the DICT of the settled payment, through the DICT's control API.
func (e *env) waitForTransaction(e2e string) map[string]any {
	e.t.Helper()
	deadline := time.Now().Add(e.deadline)
	for time.Now().Before(deadline) {
		resp, err := http.Get(e.dictURL + "/mock/transactions")
		if err != nil {
			e.t.Fatal(err)
		}
		var list struct {
			Transactions []map[string]any `json:"transactions"`
		}
		err = json.NewDecoder(resp.Body).Decode(&list)
		resp.Body.Close()
		if err != nil {
			e.t.Fatal(err)
		}
		for _, tx := range list.Transactions {
			if tx["end_to_end_id"] == e2e {
				return tx
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	e.t.Fatalf("the DICT did not learn of %s from the SPI; is the SPI started with SPI_DICT_URL pointing at the DICT?", e2e)
	return nil
}

type reportAnswer struct {
	Report struct {
		ID     string `xml:"Id"`
		Status string `xml:"Status"`
	} `xml:"InfractionReport"`
}

type refundAnswer struct {
	Refund struct {
		ID                  string `xml:"Id"`
		Status              string `xml:"Status"`
		AnalysisResult      string `xml:"AnalysisResult"`
		RefundTransactionID string `xml:"RefundTransactionId"`
	} `xml:"Refund"`
}

// The MED from start to end over both mocks: a payment settles in the SPI and the DICT learns it; the payer's
// participant reports a fraud and the payee's agrees; the payer's asks for a refund; the payee's returns the money
// with a pacs.004 FR01 and closes the refund with that return.
func TestMEDEndToEnd(t *testing.T) {
	e := newEnv(t)
	const amount = "25.00"

	e2e, _ := e.payKey(ana, carlosCPF, amount)
	e.expect(e.waitForStatus(wallet, e2e), e2e, "ACSC", "")
	e.waitForStatus(alfa, e2e)

	tx := e.waitForTransaction(e2e)
	debtor, _ := tx["debtor"].(map[string]any)
	creditor, _ := tx["creditor"].(map[string]any)
	if tx["amount"] != amount || debtor["participant"] != wallet || debtor["tax_id"] != anaCPF ||
		creditor["participant"] != alfa || creditor["tax_id"] != carlosCPF || creditor["key"] != carlosCPF {
		t.Fatalf("the DICT knows the payment as %v", tx)
	}

	var report reportAnswer
	e.dictOK(wallet, http.MethodPost, "/infraction-reports", `<CreateInfractionReportRequest><Participant>`+wallet+`</Participant>`+
		`<InfractionReport><TransactionId>`+e2e+`</TransactionId><Reason>REFUND_REQUEST</Reason><SituationType>SCAM</SituationType>`+
		`<ReportDetails>Golpe do falso parente</ReportDetails></InfractionReport></CreateInfractionReportRequest>`, http.StatusCreated, &report)
	rp := "/infraction-reports/" + report.Report.ID
	e.dictOK(alfa, http.MethodPost, rp+"/acknowledge", `<AcknowledgeInfractionReportRequest><InfractionReportId>`+report.Report.ID+
		`</InfractionReportId><Participant>`+alfa+`</Participant></AcknowledgeInfractionReportRequest>`, http.StatusOK, nil)
	e.dictOK(alfa, http.MethodPost, rp+"/close", `<CloseInfractionReportRequest><InfractionReportId>`+report.Report.ID+
		`</InfractionReportId><Participant>`+alfa+`</Participant><FraudType>SCAMMER_ACCOUNT</FraudType>`+
		`<AnalysisResult>AGREED</AnalysisResult><AnalysisDetails>Conta usada em golpes</AnalysisDetails></CloseInfractionReportRequest>`, http.StatusOK, &report)
	if report.Report.Status != "CLOSED" {
		t.Fatalf("report after closing = %+v", report.Report)
	}

	var refund refundAnswer
	e.dictOK(wallet, http.MethodPost, "/refunds", `<CreateRefundRequest><Participant>`+wallet+`</Participant><Refund><TransactionId>`+e2e+
		`</TransactionId><RefundReason>FRAUD</RefundReason><RefundAmount>`+amount+`</RefundAmount></Refund></CreateRefundRequest>`, http.StatusCreated, &refund)
	if refund.Refund.Status != "OPEN" {
		t.Fatalf("refund = %+v", refund.Refund)
	}

	// Banco Alfa sends the money back in the SPI, as a return of the MED. The wallet accepts it.
	rtr := id("D", alfa)
	e.send(alfa, pacs004(alfa, wallet, rtr, e2e, amount, "FR01"))
	order := e.waitForOrder(wallet, "pacs.004", rtr)
	if !strings.Contains(string(order.body), "<Cd>FR01</Cd>") {
		t.Fatalf("forwarded return has not the reason FR01: %s", order.body)
	}
	e.send(wallet, pacs002(wallet, rtr, e2e, "ACSP"))
	e.expect(e.waitForStatus(wallet, rtr), rtr, "ACCC", "")
	e.expect(e.waitForStatus(alfa, rtr), rtr, "ACSC", "")

	fp := "/refunds/" + refund.Refund.ID
	e.dictOK(alfa, http.MethodPost, fp+"/close", `<CloseRefundRequest><RefundId>`+refund.Refund.ID+`</RefundId><Participant>`+alfa+
		`</Participant><RefundAnalysisResult>TOTALLY_ACCEPTED</RefundAnalysisResult><RefundTransactionId>`+rtr+
		`</RefundTransactionId></CloseRefundRequest>`, http.StatusOK, nil)
	e.dictOK(wallet, http.MethodGet, fp, "", http.StatusOK, &refund)
	if refund.Refund.Status != "CLOSED" || refund.Refund.AnalysisResult != "TOTALLY_ACCEPTED" || refund.Refund.RefundTransactionID != rtr {
		t.Fatalf("refund after the return = %+v", refund.Refund)
	}
}
