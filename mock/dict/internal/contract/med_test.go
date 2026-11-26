package contract_test

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/httpapi"
)

const unknownID = "00000000-0000-4000-8000-000000000000"

func init() {
	// The file documents no 400 for these operations, though their bodies have fields that can be invalid, and no 403
	// for the reads, though they ask for PI-RequestingParticipant.
	for _, op := range []string{
		"PUT /infraction-reports/{InfractionReportId}", "POST /infraction-reports/{InfractionReportId}/close",
		"POST /refunds/{RefundId}/close",
	} {
		knownGaps[op+" 400"] = "the file documents no 400, but the body has fields that can be invalid"
	}
	for _, op := range []string{"GET /infraction-reports/{InfractionReportId}", "GET /refunds/{RefundId}", "GET /fraud-markers/{FraudMarkerId}"} {
		knownGaps[op+" 403"] = "the file documents no 403, but PI-RequestingParticipant must be the caller"
	}
}

// replaceTag sets the text of every <tag> of an example.
func replaceTag(body, tag, value string) string {
	re := regexp.MustCompile(`(?s)<` + tag + `>.*?</` + tag + `>`)
	return re.ReplaceAllString(body, "<"+tag+">"+value+"</"+tag+">")
}

// The mock accepts the MED requests of the file, in the order of a report's life: report, acknowledge, close with a
// fraud marker, refund. The examples name ids and participants of their own; the ones the mock gave are put in.
func TestSpecRequestsMED(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)
	example := func(method, path string) string {
		_, ex := sp.requestBody(sp.operation(method, path))
		if ex == "" {
			t.Fatalf("%s %s has no request example", method, path)
		}
		return ex
	}
	create := example("POST", "/infraction-reports")
	reporter := between(create, "<Participant>", "</Participant>")
	txID := between(create, "<TransactionId>", "</TransactionId>")
	const counterparty = "99999011"
	if err := s.svc.AddTransaction(context.Background(), dict.Transaction{
		ID: txID, DebtorParticipant: reporter, DebtorTaxID: "11122233300", CreditorParticipant: counterparty,
		CreditorTaxID: "12345678909", CreditorKey: "abc@pix.br", Amount: 20000,
	}); err != nil {
		t.Fatal(err)
	}
	asReporter := map[string]string{httpapi.ParticipantHeader: reporter, "PI-RequestingParticipant": reporter}
	asCounterparty := map[string]string{httpapi.ParticipantHeader: counterparty, "PI-RequestingParticipant": counterparty}
	step := func(method, spec, path, body string, headers map[string]string, want int) string {
		t.Helper()
		r := s.do(method, path, body, headers)
		conforms(t, sp, method, spec, r)
		if r.status != want {
			t.Fatalf("%s %s = %d, want %d\n%s", method, path, r.status, want, r.body)
		}
		return string(r.body)
	}

	body := step("POST", "/infraction-reports", "/infraction-reports", create, asReporter, http.StatusCreated)
	id := between(body, "<Id>", "</Id>")
	path := "/infraction-reports/" + id
	withID := func(ex, participant string) string {
		return replaceTag(replaceTag(ex, "InfractionReportId", id), "Participant", participant)
	}
	step("GET", "/infraction-reports/{InfractionReportId}", path, "", asCounterparty, http.StatusOK)
	step("GET", "/infraction-reports", "/infraction-reports?Participant="+counterparty+"&IsCounterparty=true&Status=OPEN&IncludeDetails=true",
		"", asCounterparty, http.StatusOK)
	step("PUT", "/infraction-reports/{InfractionReportId}", path, withID(example("PUT", "/infraction-reports/{InfractionReportId}"), reporter),
		asReporter, http.StatusOK)
	step("POST", "/infraction-reports/{InfractionReportId}/acknowledge", path+"/acknowledge",
		withID(example("POST", "/infraction-reports/{InfractionReportId}/acknowledge"), counterparty), asCounterparty, http.StatusOK)
	body = step("POST", "/infraction-reports/{InfractionReportId}/close", path+"/close",
		withID(example("POST", "/infraction-reports/{InfractionReportId}/close"), counterparty), asCounterparty, http.StatusOK)
	marker := between(body, "<FraudMarkerId>", "</FraudMarkerId>")
	if marker == "" {
		t.Fatalf("closing with AGREED gave no fraud marker\n%s", body)
	}
	step("GET", "/fraud-markers/{FraudMarkerId}", "/fraud-markers/"+marker, "", asCounterparty, http.StatusOK)

	// refunds, the first one for the amount of the example
	body = step("POST", "/refunds", "/refunds", example("POST", "/refunds"), asReporter, http.StatusCreated)
	refund := between(body, "<Id>", "</Id>")
	step("GET", "/refunds/{RefundId}", "/refunds/"+refund, "", asCounterparty, http.StatusOK)
	step("GET", "/refunds", "/refunds?Participant="+counterparty+"&ParticipantRole=CONTESTED&Status=OPEN&IncludeDetails=true",
		"", asCounterparty, http.StatusOK)
	closeRefund := replaceTag(replaceTag(example("POST", "/refunds/{RefundId}/close"), "RefundId", refund), "Participant", counterparty)
	step("POST", "/refunds/{RefundId}/close", "/refunds/"+refund+"/close", closeRefund, asCounterparty, http.StatusOK)
	body = step("POST", "/refunds", "/refunds", example("POST", "/refunds"), asReporter, http.StatusCreated) // the other R$ 100,00
	second := between(body, "<Id>", "</Id>")
	cancelRefund := replaceTag(replaceTag(example("POST", "/refunds/{RefundId}/cancel"), "RefundId", second), "Participant", reporter)
	step("POST", "/refunds/{RefundId}/cancel", "/refunds/"+second+"/cancel", cancelRefund, asReporter, http.StatusOK)

	step("GET", "/persons/{TaxIdNumber}/statistics", "/persons/12345678909/statistics", "", asReporter, http.StatusOK)
	step("GET", "/entries/{Key}/statistics", "/entries/abc@pix.br/statistics", "", asReporter, http.StatusOK)

	step("POST", "/infraction-reports/{InfractionReportId}/cancel", path+"/cancel",
		withID(example("POST", "/infraction-reports/{InfractionReportId}/cancel"), reporter), asReporter, http.StatusOK)

	// a fraud marker made directly, by the participant of the example
	createMarker := example("POST", "/fraud-markers")
	registrant := between(createMarker, "<Participant>", "</Participant>")
	asRegistrant := map[string]string{httpapi.ParticipantHeader: registrant, "PI-RequestingParticipant": registrant}
	body = step("POST", "/fraud-markers", "/fraud-markers", createMarker, asRegistrant, http.StatusCreated)
	direct := between(body, "<Id>", "</Id>")
	step("GET", "/fraud-markers", "/fraud-markers?TaxIdNumber="+between(createMarker, "<TaxIdNumber>", "</TaxIdNumber>")+
		"&Status=REGISTERED&page.itemsPerPage=5", "", asRegistrant, http.StatusOK)
	cancelMarker := replaceTag(replaceTag(example("POST", "/fraud-markers/{FraudMarkerId}/cancel"), "FraudMarkerId", direct), "Participant", registrant)
	step("POST", "/fraud-markers/{FraudMarkerId}/cancel", "/fraud-markers/"+direct+"/cancel", cancelMarker, asRegistrant, http.StatusOK)
}

// What the mock answers to MED requests it refuses follows the file too.
func TestSpecAnswersMED(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)
	ctx := context.Background()
	const tx = "E9900000120261120100000000000001"
	if err := s.svc.AddTransaction(ctx, dict.Transaction{
		ID: tx, DebtorParticipant: requester, DebtorTaxID: "52998224725", CreditorParticipant: holder, CreditorTaxID: carlos,
		CreditorKey: carlos, Amount: 15000,
	}); err != nil {
		t.Fatal(err)
	}
	report, err := s.svc.CreateInfractionReport(ctx, requester, dict.CreateReportInput{
		Participant: requester, TransactionID: tx, Reason: dict.ReasonRefundRequest, Situation: dict.SituationScam,
	})
	if err != nil {
		t.Fatal(err)
	}
	refund, err := s.svc.CreateRefund(ctx, requester, dict.CreateRefundInput{
		Participant: requester, TransactionID: tx, Reason: dict.RefundOperationalFlaw, Amount: 1000, Details: "Duplicado",
	})
	if err != nil {
		t.Fatal(err)
	}
	marker, err := s.svc.CreateFraudMarker(ctx, holder, dict.CreateMarkerInput{
		Participant: holder, TaxID: carlos, Key: carlos, FraudType: dict.FraudMule, RequestID: "5c1f4ac6-8d2e-4f6b-9a4e-1f2d3c4b5a69",
	})
	if err != nil {
		t.Fatal(err)
	}

	asRequester := map[string]string{httpapi.ParticipantHeader: requester, "PI-RequestingParticipant": requester}
	asHolder := map[string]string{httpapi.ParticipantHeader: holder, "PI-RequestingParticipant": holder}
	asThird := map[string]string{httpapi.ParticipantHeader: "12345678", "PI-RequestingParticipant": "12345678"}
	reportBody := func(participant, txID, reason, situation string) string {
		return `<CreateInfractionReportRequest><Participant>` + participant + `</Participant><InfractionReport><TransactionId>` + txID +
			`</TransactionId><Reason>` + reason + `</Reason><SituationType>` + situation + `</SituationType></InfractionReport></CreateInfractionReportRequest>`
	}
	action := func(root, idTag, id, participant, extra string) string {
		return `<` + root + `><` + idTag + `>` + id + `</` + idTag + `><Participant>` + participant + `</Participant>` + extra + `</` + root + `>`
	}
	refundBody := func(participant, txID, reason, amount string) string {
		return `<CreateRefundRequest><Participant>` + participant + `</Participant><Refund><TransactionId>` + txID + `</TransactionId><RefundReason>` +
			reason + `</RefundReason><RefundAmount>` + amount + `</RefundAmount><RefundDetails>Duplicado</RefundDetails></Refund></CreateRefundRequest>`
	}
	markerBody := func(participant, taxID, fraudType string) string {
		return `<CreateFraudMarkerRequest><Participant>` + participant + `</Participant><FraudMarker><TaxIdNumber>` + taxID + `</TaxIdNumber><FraudType>` +
			fraudType + `</FraudType></FraudMarker><RequestId>6d2f5bd7-9e3f-4a7c-8b5f-2a3e4d5c6b7a</RequestId></CreateFraudMarkerRequest>`
	}
	rp, fp, mp := "/infraction-reports/"+report.ID, "/refunds/"+refund.ID, "/fraud-markers/"+marker.ID

	steps := []struct {
		name         string
		method, spec string
		path, body   string
		headers      map[string]string
		want         int
	}{
		{"report again", "POST", "/infraction-reports", "/infraction-reports", reportBody(requester, tx, "REFUND_REQUEST", "SCAM"), asRequester, 409},
		{"report an unknown transaction", "POST", "/infraction-reports", "/infraction-reports", reportBody(requester, "E9900000120261120100000000000099", "REFUND_REQUEST", "SCAM"), asRequester, 400},
		{"report as the payee", "POST", "/infraction-reports", "/infraction-reports", reportBody(holder, tx, "REFUND_REQUEST", "SCAM"), asHolder, 403},
		{"report with a bad situation", "POST", "/infraction-reports", "/infraction-reports", reportBody(requester, tx, "REFUND_REQUEST", "UNKNOWN"), asRequester, 400},
		{"get a report of others", "GET", "/infraction-reports/{InfractionReportId}", rp, "", asThird, 404},
		{"get a report with another PI-RequestingParticipant", "GET", "/infraction-reports/{InfractionReportId}", rp, "", map[string]string{httpapi.ParticipantHeader: requester}, 403},
		{"list the reports of another participant", "GET", "/infraction-reports", "/infraction-reports?Participant=" + holder, "", asRequester, 403},
		{"list with a bad limit", "GET", "/infraction-reports", "/infraction-reports?Participant=" + requester + "&Limit=500", "", asRequester, 400},
		{"update as the counterparty", "PUT", "/infraction-reports/{InfractionReportId}", rp, action("UpdateInfractionReportRequest", "InfractionReportId", report.ID, holder, ""), asHolder, 403},
		{"update with a bad situation", "PUT", "/infraction-reports/{InfractionReportId}", rp, action("UpdateInfractionReportRequest", "InfractionReportId", report.ID, requester, "<SituationType>UNKNOWN</SituationType>"), asRequester, 400},
		{"update an unknown report", "PUT", "/infraction-reports/{InfractionReportId}", "/infraction-reports/" + unknownID, action("UpdateInfractionReportRequest", "InfractionReportId", unknownID, requester, ""), asRequester, 404},
		{"acknowledge as the reporter", "POST", "/infraction-reports/{InfractionReportId}/acknowledge", rp + "/acknowledge", action("AcknowledgeInfractionReportRequest", "InfractionReportId", report.ID, requester, ""), asRequester, 403},
		{"close before acknowledging", "POST", "/infraction-reports/{InfractionReportId}/close", rp + "/close", action("CloseInfractionReportRequest", "InfractionReportId", report.ID, holder, "<AnalysisResult>DISAGREED</AnalysisResult>"), asHolder, 409},
		{"acknowledge", "POST", "/infraction-reports/{InfractionReportId}/acknowledge", rp + "/acknowledge", action("AcknowledgeInfractionReportRequest", "InfractionReportId", report.ID, holder, ""), asHolder, 200},
		{"close AGREED without a fraud type", "POST", "/infraction-reports/{InfractionReportId}/close", rp + "/close", action("CloseInfractionReportRequest", "InfractionReportId", report.ID, holder, "<AnalysisResult>AGREED</AnalysisResult>"), asHolder, 400},
		{"close DISAGREED", "POST", "/infraction-reports/{InfractionReportId}/close", rp + "/close", action("CloseInfractionReportRequest", "InfractionReportId", report.ID, holder, "<AnalysisResult>DISAGREED</AnalysisResult>"), asHolder, 200},
		{"close again another way", "POST", "/infraction-reports/{InfractionReportId}/close", rp + "/close", action("CloseInfractionReportRequest", "InfractionReportId", report.ID, holder, "<AnalysisResult>AGREED</AnalysisResult><FraudType>MULE_ACCOUNT</FraudType>"), asHolder, 409},
		{"cancel as the counterparty", "POST", "/infraction-reports/{InfractionReportId}/cancel", rp + "/cancel", action("CancelInfractionReportRequest", "InfractionReportId", report.ID, holder, ""), asHolder, 403},
		{"cancel an unknown report", "POST", "/infraction-reports/{InfractionReportId}/cancel", "/infraction-reports/" + unknownID + "/cancel", action("CancelInfractionReportRequest", "InfractionReportId", unknownID, requester, ""), asRequester, 404},

		{"refund for fraud without an agreed report", "POST", "/refunds", "/refunds", refundBody(requester, tx, "FRAUD", "10.00"), asRequester, 400},
		{"a second open refund", "POST", "/refunds", "/refunds", refundBody(requester, tx, "OPERATIONAL_FLAW", "10.00"), asRequester, 409},
		{"refund as the payee", "POST", "/refunds", "/refunds", refundBody(holder, tx, "OPERATIONAL_FLAW", "10.00"), asHolder, 403},
		{"refund with a bad amount", "POST", "/refunds", "/refunds", refundBody(requester, tx, "OPERATIONAL_FLAW", "1.234"), asRequester, 400},
		{"get a refund of others", "GET", "/refunds/{RefundId}", fp, "", asThird, 404},
		{"get a refund with another PI-RequestingParticipant", "GET", "/refunds/{RefundId}", fp, "", map[string]string{httpapi.ParticipantHeader: requester}, 403},
		{"list refunds without a role", "GET", "/refunds", "/refunds?Participant=" + requester, "", asRequester, 400},
		{"list the refunds of another participant", "GET", "/refunds", "/refunds?Participant=" + holder + "&ParticipantRole=CONTESTED", "", asRequester, 403},
		{"close a refund as the requesting participant", "POST", "/refunds/{RefundId}/close", fp + "/close", action("CloseRefundRequest", "RefundId", refund.ID, requester, "<RefundAnalysisResult>REJECTED</RefundAnalysisResult><RefundRejectionReason>OTHER</RefundRejectionReason>"), asRequester, 403},
		{"accept a refund without its return", "POST", "/refunds/{RefundId}/close", fp + "/close", action("CloseRefundRequest", "RefundId", refund.ID, holder, "<RefundAnalysisResult>TOTALLY_ACCEPTED</RefundAnalysisResult>"), asHolder, 400},
		{"close an unknown refund", "POST", "/refunds/{RefundId}/close", "/refunds/" + unknownID + "/close", action("CloseRefundRequest", "RefundId", unknownID, holder, "<RefundAnalysisResult>REJECTED</RefundAnalysisResult><RefundRejectionReason>OTHER</RefundRejectionReason>"), asHolder, 404},
		{"reject a refund", "POST", "/refunds/{RefundId}/close", fp + "/close", action("CloseRefundRequest", "RefundId", refund.ID, holder, "<RefundAnalysisResult>REJECTED</RefundAnalysisResult><RefundRejectionReason>NO_BALANCE</RefundRejectionReason>"), asHolder, 200},
		{"cancel a closed refund", "POST", "/refunds/{RefundId}/cancel", fp + "/cancel", action("CancelRefundRequest", "RefundId", refund.ID, requester, ""), asRequester, 409},
		{"cancel a refund as the contested participant", "POST", "/refunds/{RefundId}/cancel", fp + "/cancel", action("CancelRefundRequest", "RefundId", refund.ID, holder, ""), asHolder, 403},
		{"cancel an unknown refund", "POST", "/refunds/{RefundId}/cancel", "/refunds/" + unknownID + "/cancel", action("CancelRefundRequest", "RefundId", unknownID, requester, ""), asRequester, 404},

		{"marker with invalid fields", "POST", "/fraud-markers", "/fraud-markers", markerBody(holder, "123", "UNKNOWN"), asHolder, 400},
		{"marker for another participant", "POST", "/fraud-markers", "/fraud-markers", markerBody(holder, carlos, "MULE_ACCOUNT"), asRequester, 403},
		{"marker with a used RequestId", "POST", "/fraud-markers", "/fraud-markers", strings.Replace(markerBody(holder, carlos, "OTHER"), "6d2f5bd7-9e3f-4a7c-8b5f-2a3e4d5c6b7a", marker.RequestID, 1), asHolder, 400},
		{"get a marker of others", "GET", "/fraud-markers/{FraudMarkerId}", mp, "", asRequester, 404},
		{"get a marker with another PI-RequestingParticipant", "GET", "/fraud-markers/{FraudMarkerId}", mp, "", map[string]string{httpapi.ParticipantHeader: holder}, 403},
		{"list markers without a tax id", "GET", "/fraud-markers", "/fraud-markers", "", asHolder, 400},
		{"list the markers of another participant", "GET", "/fraud-markers", "/fraud-markers?TaxIdNumber=" + carlos + "&Participant=" + requester, "", asHolder, 403},
		{"cancel a marker of others", "POST", "/fraud-markers/{FraudMarkerId}/cancel", mp + "/cancel", action("CancelFraudMarkerRequest", "FraudMarkerId", marker.ID, requester, ""), asRequester, 404},
		{"cancel a marker for another participant", "POST", "/fraud-markers/{FraudMarkerId}/cancel", mp + "/cancel", action("CancelFraudMarkerRequest", "FraudMarkerId", marker.ID, requester, ""), asHolder, 403},
		{"cancel a marker", "POST", "/fraud-markers/{FraudMarkerId}/cancel", mp + "/cancel", action("CancelFraudMarkerRequest", "FraudMarkerId", marker.ID, holder, ""), asHolder, 200},

		{"statistics of a person", "GET", "/persons/{TaxIdNumber}/statistics", "/persons/" + carlos + "/statistics", "", asRequester, 200},
		{"statistics with another PI-RequestingParticipant", "GET", "/persons/{TaxIdNumber}/statistics", "/persons/" + carlos + "/statistics", "", map[string]string{httpapi.ParticipantHeader: requester}, 403},
		{"statistics of a key", "GET", "/entries/{Key}/statistics", "/entries/" + carlos + "/statistics", "", asRequester, 200},
		{"statistics of a key nobody has", "GET", "/entries/{Key}/statistics", "/entries/nobody@example.com/statistics", "", asRequester, 200},
	}
	for _, st := range steps {
		t.Run(st.name, func(t *testing.T) {
			r := s.do(st.method, st.path, st.body, st.headers)
			if r.status != st.want {
				t.Fatalf("status = %d, want %d\n%s", r.status, st.want, r.body)
			}
			conforms(t, sp, st.method, st.spec, r)
		})
	}
}
