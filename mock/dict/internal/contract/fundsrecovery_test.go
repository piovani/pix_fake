package contract_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/httpapi"
)

func init() {
	for _, op := range []string{"PUT /funds-recoveries/{FundsRecoveryId}", "POST /funds-recoveries/{FundsRecoveryId}/refund"} {
		knownGaps[op+" 400"] = "the file documents no 400, but its errors (FundsRecoveryInvalid, FundsRecoveryOperationInvalid) are 400"
	}
	knownGaps["POST /funds-recoveries/{FundsRecoveryId}/cancel 404"] = "the file documents no 404, but the funds recovery may not exist"
	for _, op := range []string{"GET /funds-recoveries/{FundsRecoveryId}", "GET /funds-recoveries/{FundsRecoveryId}/tracking-graph"} {
		knownGaps[op+" 403"] = "the file documents no 403, but PI-RequestingParticipant must be the caller"
	}
	for _, op := range []string{"POST /funds-recoveries/{FundsRecoveryId}/tracking-graph", "POST /funds-recoveries/{FundsRecoveryId}/block"} {
		knownGaps[op+" 410"] = "the file lists DeprecatedResource (410) among the errors, but documents no 410"
	}
}

// The mock accepts the funds recovery requests of the file, in the order of its life: created (tracked and blocked),
// updated, analysed, refunded, completed. The root of the example is paid by its participant to a mule, who passes
// part of it on.
func TestSpecRequestsFundsRecovery(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)
	ctx := context.Background()
	example := func(method, path string) string {
		_, ex := sp.requestBody(sp.operation(method, path))
		if ex == "" {
			t.Fatalf("%s %s has no request example", method, path)
		}
		return ex
	}
	create := example("POST", "/funds-recoveries")
	reporter := between(create, "<Participant>", "</Participant>")
	root := between(create, "<RootTransactionId>", "</RootTransactionId>")
	const mulePSP, nextPSP = "99999011", "99999012"
	at := time.Now().UTC().Add(-2 * time.Hour)
	for _, tr := range []dict.Transaction{
		{ID: root, DebtorParticipant: reporter, DebtorTaxID: "11122233300", CreditorParticipant: mulePSP, CreditorTaxID: "12345678909", Amount: 100000, SettlementTime: at},
		{ID: "E9999901112341234123412345678901", DebtorParticipant: mulePSP, DebtorTaxID: "12345678909", CreditorParticipant: nextPSP,
			CreditorTaxID: "11222333000181", Amount: 60000, SettlementTime: at.Add(time.Hour)},
	} {
		if err := s.svc.AddTransaction(ctx, tr); err != nil {
			t.Fatal(err)
		}
	}
	as := func(p string) map[string]string {
		return map[string]string{httpapi.ParticipantHeader: p, "PI-RequestingParticipant": p}
	}
	step := func(method, spec, path, body string, headers map[string]string, want int) string {
		t.Helper()
		r := s.do(method, path, body, headers)
		conforms(t, sp, method, spec, r)
		if r.status != want {
			t.Fatalf("%s %s = %d, want %d\n%s", method, path, r.status, want, r.body)
		}
		return string(r.body)
	}

	body := step("POST", "/funds-recoveries", "/funds-recoveries", create, as(reporter), http.StatusCreated)
	id := between(body, "<Id>", "</Id>")
	path := "/funds-recoveries/" + id
	withID := func(ex string) string { return replaceTag(ex, "FundsRecoveryId", id) }
	step("GET", "/funds-recoveries/{FundsRecoveryId}", path, "", as(nextPSP), http.StatusOK)
	step("GET", "/funds-recoveries/{FundsRecoveryId}/tracking-graph", path+"/tracking-graph", "", as(mulePSP), http.StatusOK)
	step("PUT", "/funds-recoveries/{FundsRecoveryId}", path, withID(example("PUT", "/funds-recoveries/{FundsRecoveryId}")), as(reporter), http.StatusOK)
	body = step("GET", "/funds-recoveries/{FundsRecoveryId}/infraction-reports", path+"/infraction-reports", "", as(reporter), http.StatusOK)
	if n := len(between(body, "<InfractionReports>", "</InfractionReports>")); n == 0 {
		t.Fatalf("no reports\n%s", body)
	}
	reports, err := s.svc.FundsRecoveryReports(ctx, reporter, id)
	if err != nil || len(reports) != 2 {
		t.Fatalf("reports = %+v, %v", reports, err)
	}
	for _, r := range reports {
		if _, err := s.svc.AcknowledgeInfractionReport(ctx, r.Counterparty, r.Counterparty, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.svc.CloseInfractionReport(ctx, r.Counterparty, r.ID, dict.CloseReportInput{
			Participant: r.Counterparty, Result: dict.AnalysisAgreed, FraudType: dict.FraudMule,
		}); err != nil {
			t.Fatal(err)
		}
	}
	step("GET", "/event-notifications", "/event-notifications?Participant="+reporter+"&IncludeIndirectParticipants=false", "", as(reporter), http.StatusOK)
	step("POST", "/funds-recoveries/{FundsRecoveryId}/refund", path+"/refund", withID(example("POST", "/funds-recoveries/{FundsRecoveryId}/refund")),
		as(reporter), http.StatusOK)
	step("GET", "/funds-recoveries/{FundsRecoveryId}/refunds", path+"/refunds", "", as(reporter), http.StatusOK)
	step("POST", "/funds-recoveries/{FundsRecoveryId}/tracking-graph", path+"/tracking-graph",
		withID(example("POST", "/funds-recoveries/{FundsRecoveryId}/tracking-graph")), as(reporter), http.StatusGone)
	step("POST", "/funds-recoveries/{FundsRecoveryId}/block", path+"/block", withID(example("POST", "/funds-recoveries/{FundsRecoveryId}/block")),
		as(reporter), http.StatusGone)
	step("POST", "/funds-recoveries/{FundsRecoveryId}/cancel", path+"/cancel", withID(example("POST", "/funds-recoveries/{FundsRecoveryId}/cancel")),
		as(reporter), http.StatusOK)
}

// What the mock answers to funds recovery requests it refuses follows the file too.
func TestSpecAnswersFundsRecovery(t *testing.T) {
	sp := loadSpec(t)
	s := newServer(t)
	ctx := context.Background()
	const root = "E9900000120261120100000000000002"
	if err := s.svc.AddTransaction(ctx, dict.Transaction{
		ID: root, DebtorParticipant: requester, DebtorTaxID: "52998224725", CreditorParticipant: holder, CreditorTaxID: carlos, Amount: 15000,
	}); err != nil {
		t.Fatal(err)
	}
	f, err := s.svc.CreateFundsRecovery(ctx, requester, dict.CreateRecoveryInput{
		Participant: requester, RootTransactionID: root, Situation: dict.SituationScam,
		Contact: &dict.Contact{Email: "fraude@banco.example.com", Phone: "+5561988887777"},
	})
	if err != nil {
		t.Fatal(err)
	}
	asRequester := map[string]string{httpapi.ParticipantHeader: requester, "PI-RequestingParticipant": requester}
	asHolder := map[string]string{httpapi.ParticipantHeader: holder, "PI-RequestingParticipant": holder}
	asThird := map[string]string{httpapi.ParticipantHeader: "12345678", "PI-RequestingParticipant": "12345678"}
	createBody := func(participant, txID, params string) string {
		return `<CreateFundsRecoveryRequest><Participant>` + participant + `</Participant><FundsRecovery><RootTransactionId>` + txID +
			`</RootTransactionId><SituationType>SCAM</SituationType><ContactInformation><Email>abc@pix.br</Email><Phone>+5561988887777</Phone>` +
			`</ContactInformation></FundsRecovery>` + params + `</CreateFundsRecoveryRequest>`
	}
	action := func(root, id, participant, extra string) string {
		return `<` + root + `><Participant>` + participant + `</Participant><FundsRecoveryId>` + id + `</FundsRecoveryId>` + extra + `</` + root + `>`
	}
	fp := "/funds-recoveries/" + f.ID
	unknown := "/funds-recoveries/" + unknownID

	steps := []struct {
		name         string
		method, spec string
		path, body   string
		headers      map[string]string
		want         int
	}{
		{"again on the same root", "POST", "/funds-recoveries", "/funds-recoveries", createBody(requester, root, ""), asRequester, 400},
		{"an unknown root", "POST", "/funds-recoveries", "/funds-recoveries", createBody(requester, "E9900000120261120100000000000099", ""), asRequester, 400},
		{"as the payee", "POST", "/funds-recoveries", "/funds-recoveries", createBody(holder, root, ""), asHolder, 403},
		{"graph parameters out of range", "POST", "/funds-recoveries", "/funds-recoveries", createBody(requester, root,
			`<TrackingGraphParameters><MaxHops>99</MaxHops><HopWindow>P30D</HopWindow></TrackingGraphParameters>`), asRequester, 400},
		{"graph parameters that do not parse", "POST", "/funds-recoveries", "/funds-recoveries", createBody(requester, root,
			`<TrackingGraphParameters><HopWindow>24h</HopWindow></TrackingGraphParameters>`), asRequester, 400},
		{"get as a stranger", "GET", "/funds-recoveries/{FundsRecoveryId}", fp, "", asThird, 404},
		{"get with another PI-RequestingParticipant", "GET", "/funds-recoveries/{FundsRecoveryId}", fp, "", map[string]string{httpapi.ParticipantHeader: requester}, 403},
		{"graph as a stranger", "GET", "/funds-recoveries/{FundsRecoveryId}/tracking-graph", fp + "/tracking-graph", "", asThird, 404},
		{"update as the counterparty", "PUT", "/funds-recoveries/{FundsRecoveryId}", fp, action("UpdateFundsRecoveryRequest", f.ID, holder, ""), asHolder, 403},
		{"update with a bad situation", "PUT", "/funds-recoveries/{FundsRecoveryId}", fp, action("UpdateFundsRecoveryRequest", f.ID, requester, "<SituationType>UNKNOWN</SituationType>"), asRequester, 400},
		{"update an unknown one", "PUT", "/funds-recoveries/{FundsRecoveryId}", unknown, action("UpdateFundsRecoveryRequest", unknownID, requester, ""), asRequester, 404},
		{"reports as the counterparty", "GET", "/funds-recoveries/{FundsRecoveryId}/infraction-reports", fp + "/infraction-reports", "", asHolder, 403},
		{"refunds before any was asked for", "GET", "/funds-recoveries/{FundsRecoveryId}/refunds", fp + "/refunds", "", asRequester, 400},
		{"refund before it is analysed", "POST", "/funds-recoveries/{FundsRecoveryId}/refund", fp + "/refund", action("RefundFundsRecoveryRequest", f.ID, requester, ""), asRequester, 400},
		{"refund an unknown one", "POST", "/funds-recoveries/{FundsRecoveryId}/refund", unknown + "/refund", action("RefundFundsRecoveryRequest", unknownID, requester, ""), asRequester, 404},
		{"block", "POST", "/funds-recoveries/{FundsRecoveryId}/block", fp + "/block", action("BlockFundsRecoveryRequest", f.ID, requester, "<PrioritizationStrategy>STANDARD_ALGORITHM</PrioritizationStrategy>"), asRequester, 410},
		{"cancel as the counterparty", "POST", "/funds-recoveries/{FundsRecoveryId}/cancel", fp + "/cancel", action("CancelFundsRecoveryRequest", f.ID, holder, ""), asHolder, 403},
		{"cancel an unknown one", "POST", "/funds-recoveries/{FundsRecoveryId}/cancel", unknown + "/cancel", action("CancelFundsRecoveryRequest", unknownID, requester, ""), asRequester, 404},
		{"cancel", "POST", "/funds-recoveries/{FundsRecoveryId}/cancel", fp + "/cancel", action("CancelFundsRecoveryRequest", f.ID, requester, ""), asRequester, 200},
		{"events of another participant", "GET", "/event-notifications", "/event-notifications?Participant=" + holder, "", asRequester, 403},
		{"events with a bad cursor", "GET", "/event-notifications", "/event-notifications?Participant=" + holder + "&Cursor=%25%25", "", asHolder, 400},
		{"events", "GET", "/event-notifications", "/event-notifications?Participant=" + holder, "", asHolder, 200},
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
