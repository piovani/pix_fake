package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"google.golang.org/grpc/codes"

	walletv1 "pixfake/wallet/gen/wallet/v1"
)

// The MED with the wallet as the payer's participant: Ana pays Carlos at Banco Alfa and contests the payment. The
// wallet reports it in the DICT; Banco Alfa (the test) analyses it; when it agrees the wallet asks for the refund, Banco
// Alfa returns the money in the SPI (FR01) and closes the refund, and the contest ends REFUNDED.

// paidToCarlos has Ana pay Carlos cents and waits until it settled and the DICT knows it.
func (e env) paidToCarlos(t *testing.T, cents int64) *walletv1.Payment {
	t.Helper()
	p, err := e.pay(t, e.resolve(t, carlosPhone).ResolutionId, cents, "")
	if err != nil {
		t.Fatalf("CreatePayment: %v", err)
	}
	done := e.waitFinished(t, p.Id, 60*time.Second)
	if done.Status != walletv1.PaymentStatus_PAYMENT_STATUS_SETTLED {
		t.Fatalf("payment: %v", done)
	}
	e.waitDICTKnows(t, done.EndToEndId)
	return done
}

// reportAgainstBank finds the open report the wallet made on the transaction, as Banco Alfa sees it.
func (e env) reportAgainstBank(t *testing.T, e2e string) string {
	t.Helper()
	var out struct {
		Reports []struct {
			ID            string `xml:"Id"`
			TransactionID string `xml:"TransactionId"`
		} `xml:"InfractionReports>InfractionReport"`
	}
	e.asBank(t, http.MethodGet, "/infraction-reports?Participant="+bankAlfa+"&IsCounterparty=true&Status=OPEN,ACKNOWLEDGED&Limit=200", "", http.StatusOK, &out)
	for _, r := range out.Reports {
		if r.TransactionID == e2e {
			return r.ID
		}
	}
	t.Fatalf("Banco Alfa has no report on %s", e2e)
	return ""
}

func (e env) waitContest(t *testing.T, c walletv1.ContestServiceClient, id string, want walletv1.ContestStatus) *walletv1.Contest {
	t.Helper()
	var got *walletv1.Contest
	eventually(t, "contest "+id+" becoming "+want.String(), func() bool {
		res, err := c.GetContest(ctx(t), &walletv1.GetContestRequest{ContestId: id})
		if err != nil {
			t.Fatal(err)
		}
		got = res.Contest
		return got.Status == want
	})
	return got
}

func (e env) bankAnalyses(t *testing.T, report, result string) {
	t.Helper()
	e.asBank(t, http.MethodPost, "/infraction-reports/"+report+"/acknowledge", `<AcknowledgeInfractionReportRequest><InfractionReportId>`+report+
		`</InfractionReportId><Participant>`+bankAlfa+`</Participant></AcknowledgeInfractionReportRequest>`, http.StatusOK, nil)
	fraud := ""
	if result == "AGREED" {
		fraud = `<FraudType>SCAMMER_ACCOUNT</FraudType>`
	}
	e.asBank(t, http.MethodPost, "/infraction-reports/"+report+"/close", `<CloseInfractionReportRequest><InfractionReportId>`+report+
		`</InfractionReportId><Participant>`+bankAlfa+`</Participant>`+fraud+`<AnalysisResult>`+result+`</AnalysisResult></CloseInfractionReportRequest>`, http.StatusOK, nil)
}

func TestContestRefunded(t *testing.T) {
	e := needMED(t)
	contests := e.contests
	paid := e.paidToCarlos(t, 4500)
	before := e.balance(t)

	res, err := contests.ContestPayment(ctx(t), &walletv1.ContestPaymentRequest{
		PaymentId: paid.Id, Situation: walletv1.ContestSituation_CONTEST_SITUATION_SCAM, Details: "Golpe do falso parente", RequestId: requestID(t),
	})
	if err != nil {
		t.Fatalf("ContestPayment: %v", err)
	}
	c := res.Contest
	if c.Status != walletv1.ContestStatus_CONTEST_STATUS_REPORTED || c.AmountCents != 4500 || c.PaymentId != paid.Id {
		t.Fatalf("contest: %v", c)
	}
	_, err = contests.ContestPayment(ctx(t), &walletv1.ContestPaymentRequest{PaymentId: paid.Id, Situation: walletv1.ContestSituation_CONTEST_SITUATION_SCAM, RequestId: requestID(t)})
	wantCode(t, "a second contest", err, codes.FailedPrecondition)

	report := e.reportAgainstBank(t, paid.EndToEndId)
	e.bankAnalyses(t, report, "AGREED")
	e.waitContest(t, contests, c.Id, walletv1.ContestStatus_CONTEST_STATUS_REFUND_REQUESTED)

	var refunds struct {
		Refunds []dictRefund `xml:"Refunds>Refund"`
	}
	e.asBank(t, http.MethodGet, "/refunds?Participant="+bankAlfa+"&ParticipantRole=CONTESTED&Status=OPEN&Limit=200", "", http.StatusOK, &refunds)
	refundID := ""
	for _, f := range refunds.Refunds {
		var full struct {
			Refund struct {
				ID            string `xml:"Id"`
				TransactionID string `xml:"TransactionId"`
				Amount        string `xml:"RefundAmount"`
			} `xml:"Refund"`
		}
		e.asBank(t, http.MethodGet, "/refunds/"+f.ID, "", http.StatusOK, &full)
		if full.Refund.TransactionID == paid.EndToEndId {
			if full.Refund.Amount != "45.00" {
				t.Fatalf("refund asked for %s", full.Refund.Amount)
			}
			refundID = f.ID
		}
	}
	if refundID == "" {
		t.Fatal("the wallet asked for no refund")
	}

	// Banco Alfa sends the money back in the SPI as a return of the MED, and closes the refund with it.
	resp, err := http.Post(e.spi+"/mock/inbound/returns", "application/json",
		bytes.NewReader([]byte(`{"end_to_end_id":"`+paid.EndToEndId+`","reason":"FR01"}`)))
	if err != nil {
		t.Fatal(err)
	}
	var ret struct {
		ID string `json:"id"`
	}
	err = json.NewDecoder(resp.Body).Decode(&ret)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated || err != nil || ret.ID == "" {
		t.Fatalf("the simulated bank could not return: %v", err)
	}
	e.mockPayment(t, ret.ID, func(p map[string]any) bool { return p["status"] == "SETTLED" })
	e.asBank(t, http.MethodPost, "/refunds/"+refundID+"/close", `<CloseRefundRequest><RefundId>`+refundID+`</RefundId><Participant>`+bankAlfa+
		`</Participant><RefundAnalysisResult>TOTALLY_ACCEPTED</RefundAnalysisResult><RefundTransactionId>`+ret.ID+
		`</RefundTransactionId></CloseRefundRequest>`, http.StatusOK, nil)

	done := e.waitContest(t, contests, c.Id, walletv1.ContestStatus_CONTEST_STATUS_REFUNDED)
	if done.RefundedCents != 4500 || done.ReturnEndToEndId != ret.ID {
		t.Fatalf("contest: %v", done)
	}
	eventually(t, "the return credited", func() bool { return e.balance(t).AvailableCents == before.AvailableCents+4500 })
	list, err := contests.ListContests(ctx(t), &walletv1.ListContestsRequest{AccountId: paid.AccountId})
	if err != nil || len(list.Contests) == 0 || list.Contests[0].Id != c.Id {
		t.Fatalf("ListContests: %v, %v", list, err)
	}
}

func TestContestRejected(t *testing.T) {
	e := needMED(t)
	contests := e.contests
	paid := e.paidToCarlos(t, 300)
	res, err := contests.ContestPayment(ctx(t), &walletv1.ContestPaymentRequest{
		PaymentId: paid.Id, Situation: walletv1.ContestSituation_CONTEST_SITUATION_ACCOUNT_TAKEOVER, RequestId: requestID(t),
	})
	if err != nil {
		t.Fatalf("ContestPayment: %v", err)
	}
	e.bankAnalyses(t, e.reportAgainstBank(t, paid.EndToEndId), "DISAGREED")
	if c := e.waitContest(t, contests, res.Contest.Id, walletv1.ContestStatus_CONTEST_STATUS_REJECTED); c.Rejection == "" {
		t.Fatalf("contest: %v", c)
	}
}

func TestContestCancelled(t *testing.T) {
	e := needMED(t)
	contests := e.contests
	paid := e.paidToCarlos(t, 200)
	res, err := contests.ContestPayment(ctx(t), &walletv1.ContestPaymentRequest{
		PaymentId: paid.Id, Situation: walletv1.ContestSituation_CONTEST_SITUATION_OTHER, Details: "Paguei errado", RequestId: requestID(t),
	})
	if err != nil {
		t.Fatalf("ContestPayment: %v", err)
	}
	report := e.reportAgainstBank(t, paid.EndToEndId)
	cancelled, err := contests.CancelContest(ctx(t), &walletv1.CancelContestRequest{ContestId: res.Contest.Id})
	if err != nil || cancelled.Contest.Status != walletv1.ContestStatus_CONTEST_STATUS_CANCELLED {
		t.Fatalf("CancelContest: %v, %v", cancelled, err)
	}
	if r := e.report(t, report); r.Status != "CANCELLED" {
		t.Fatalf("report in the DICT: %+v", r)
	}
	_, err = contests.ContestPayment(ctx(t), &walletv1.ContestPaymentRequest{PaymentId: paid.Id, Situation: walletv1.ContestSituation_CONTEST_SITUATION_UNSPECIFIED, RequestId: requestID(t)})
	wantCode(t, "no situation", err, codes.InvalidArgument)
}
