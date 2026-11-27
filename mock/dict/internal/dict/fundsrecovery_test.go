package dict_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
)

const (
	finalPSP = "55667788"
	mule2Tax = "52998224725"    // a natural person at thirdPSP
	mule3Tax = "11222333000181" // a legal person at thirdPSP
	finalTax = "98765432100"
)

var t0 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

// e2e builds an EndToEndId of the transactions of the chain.
func e2e(n string) string { return "E1234567820261120100000000000" + n }

// chain is the money of a scam going from the victim (payerPSP) to the first mule (payeePSP), and on:
//
//	T1 victim → mule1 R$ 1.000,00 at t0           (the root)
//	T2 mule1  → mule2 R$   600,00 at t0+1h        depth 2, blocks 600
//	T3 mule1  → mule3 R$   600,00 at t0+2h        depth 2, blocks the 400 left of T1
//	T4 mule1  → mule2 R$   100,00 at t0+30h       out of the 24h window
//	T5 mule2  → final R$    50,00 at t0+3h        below MinTransactionAmount
//	T6 mule2  → final R$   500,00 at t0+3h        depth 3, blocks 500
//	T7 mule2  → mule2 R$   500,00 one hour before T2 came in: not money of the scam
func chain(t *testing.T) *dict.Service {
	t.Helper()
	svc := newService()
	for _, tr := range []dict.Transaction{
		{ID: e2e("001"), DebtorParticipant: payerPSP, DebtorTaxID: payerTax, CreditorParticipant: payeePSP, CreditorTaxID: payeeTax, CreditorKey: payeeKey, Amount: 100000, SettlementTime: t0},
		{ID: e2e("002"), DebtorParticipant: payeePSP, DebtorTaxID: payeeTax, CreditorParticipant: thirdPSP, CreditorTaxID: mule2Tax, Amount: 60000, SettlementTime: t0.Add(time.Hour)},
		{ID: e2e("003"), DebtorParticipant: payeePSP, DebtorTaxID: payeeTax, CreditorParticipant: thirdPSP, CreditorTaxID: mule3Tax, Amount: 60000, SettlementTime: t0.Add(2 * time.Hour)},
		{ID: e2e("004"), DebtorParticipant: payeePSP, DebtorTaxID: payeeTax, CreditorParticipant: thirdPSP, CreditorTaxID: mule2Tax, Amount: 10000, SettlementTime: t0.Add(30 * time.Hour)},
		{ID: e2e("005"), DebtorParticipant: thirdPSP, DebtorTaxID: mule2Tax, CreditorParticipant: finalPSP, CreditorTaxID: finalTax, Amount: 5000, SettlementTime: t0.Add(3 * time.Hour)},
		{ID: e2e("006"), DebtorParticipant: thirdPSP, DebtorTaxID: mule2Tax, CreditorParticipant: finalPSP, CreditorTaxID: finalTax, Amount: 50000, SettlementTime: t0.Add(3 * time.Hour)},
		{ID: e2e("007"), DebtorParticipant: thirdPSP, DebtorTaxID: mule2Tax, CreditorParticipant: finalPSP, CreditorTaxID: finalTax, Amount: 50000, SettlementTime: t0},
	} {
		if err := svc.AddTransaction(context.Background(), tr); err != nil {
			t.Fatal(err)
		}
	}
	svc.SetClock(func() time.Time { return t0.Add(48 * time.Hour) })
	return svc
}

func ptr[T any](v T) *T { return &v }

func recoveryInput() dict.CreateRecoveryInput {
	return dict.CreateRecoveryInput{
		Participant: payerPSP, RootTransactionID: e2e("001"), Situation: dict.SituationScam, ReportDetails: "QR Code falso",
		Contact: &dict.Contact{Email: "fraude@banco.example.com", Phone: "+5561988887777"},
		Graph:   dict.GraphInput{MinAmount: ptr(int64(10000))},
	}
}

// reportsByTx are the reports of a funds recovery, by transaction.
func reportsByTx(t *testing.T, svc *dict.Service, id string) map[string]dict.InfractionReport {
	t.Helper()
	reports, err := svc.FundsRecoveryReports(context.Background(), payerPSP, id)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]dict.InfractionReport{}
	for _, r := range reports {
		out[r.TransactionID] = r
	}
	return out
}

func closeReport(t *testing.T, svc *dict.Service, r dict.InfractionReport, result dict.AnalysisResult) dict.InfractionReport {
	t.Helper()
	ctx := context.Background()
	if _, err := svc.AcknowledgeInfractionReport(ctx, r.Counterparty, r.Counterparty, r.ID); err != nil {
		t.Fatal(err)
	}
	in := dict.CloseReportInput{Participant: r.Counterparty, Result: result}
	if result == dict.AnalysisAgreed {
		in.FraudType = dict.FraudMule
	}
	r, err := svc.CloseInfractionReport(ctx, r.Counterparty, r.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func events(t *testing.T, svc *dict.Service, participant string) []dict.EventType {
	t.Helper()
	page, err := svc.ListEventNotifications(context.Background(), participant, dict.ListEventsInput{Participant: participant})
	if err != nil {
		t.Fatal(err)
	}
	var out []dict.EventType
	for _, e := range page.Events {
		out = append(out, e.Type)
	}
	return out
}

// A funds recovery from beginning to end: tracked, every report analysed, refunded and completed.
func TestFundsRecovery(t *testing.T) {
	ctx := context.Background()
	svc := chain(t)

	f, err := svc.CreateFundsRecovery(ctx, payerPSP, recoveryInput())
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != dict.RecoveryAwaitingAnalysis || f.Reporter != payerPSP {
		t.Fatalf("created = %+v", f)
	}
	g := f.Graph
	if g.Parameters != (dict.GraphParameters{MinAmount: 10000, MaxTransactions: 500, HopWindow: 24 * time.Hour, MaxHops: 5}) {
		t.Fatalf("parameters = %+v", g.Parameters)
	}
	var tracked []string
	for _, tr := range g.Transactions {
		tracked = append(tracked, tr.ID[len(tr.ID)-3:]+"@"+string(rune('0'+tr.Depth)))
	}
	if !slices.Equal(tracked, []string{"001@1", "002@2", "003@2", "006@3"}) {
		t.Fatalf("graph transactions = %v", tracked)
	}
	if len(g.Persons) != 5 || g.Persons[3] != (dict.GraphPerson{ID: 4, Legal: true}) || len(g.Accounts) != 5 {
		t.Fatalf("persons = %+v, accounts = %+v", g.Persons, g.Accounts)
	}
	if root := g.Transactions[0]; root.DebtorAccountID != 1 || root.CreditorAccountID != 2 || root.Refundable != 100000 {
		t.Fatalf("root = %+v", root)
	}

	reports := reportsByTx(t, svc, f.ID)
	for id, want := range map[string]int64{e2e("001"): 100000, e2e("002"): 60000, e2e("003"): 40000, e2e("006"): 50000} {
		r := reports[id]
		if r.Amount != want || r.FundsRecoveryID != f.ID || r.Status != dict.ReportOpen || r.Contact == nil || r.ReportDetails != "QR Code falso" {
			t.Fatalf("report of %s = %+v, want amount %d", id, r, want)
		}
	}
	if r := reports[e2e("006")]; r.Reporter != thirdPSP || r.Counterparty != finalPSP || r.Depth != 3 {
		t.Fatalf("report of the third hop = %+v", r)
	}

	// who sees it
	if _, err := svc.GetFundsRecovery(ctx, finalPSP, f.ID); err != nil {
		t.Fatalf("a counterparty cannot see it: %v", err)
	}
	wantKind(t, mustFail(svc.GetFundsRecovery(ctx, "99999999", f.ID)), dict.KindNotFound)
	wantKind(t, mustFail(svc.FundsRecoveryReports(ctx, payeePSP, f.ID)), dict.KindForbidden)

	// its reports are handled through it
	r1 := reports[e2e("001")]
	wantKind(t, mustFail(svc.CancelInfractionReport(ctx, payerPSP, payerPSP, r1.ID)), dict.KindReportOperation)
	wantKind(t, mustFail(svc.UpdateInfractionReport(ctx, payerPSP, r1.ID, dict.UpdateReportInput{Participant: payerPSP})), dict.KindReportOperation)
	in := reportInput()
	in.Participant, in.TransactionID = payeePSP, e2e("002")
	wantKind(t, mustFail(svc.CreateInfractionReport(ctx, payeePSP, in)), dict.KindConflict)
	wantKind(t, mustFail(svc.RefundFundsRecovery(ctx, payerPSP, payerPSP, f.ID)), dict.KindRecoveryOperation)
	wantKind(t, mustFail(svc.FundsRecoveryRefunds(ctx, payerPSP, f.ID)), dict.KindRecoveryOperation)

	// an update reaches the reports and their counterparties
	f, err = svc.UpdateFundsRecovery(ctx, payerPSP, f.ID, dict.UpdateRecoveryInput{
		Participant: payerPSP, Situation: dict.SituationOther, ReportDetails: ptr("Golpe do falso parente"),
	})
	if err != nil || f.Situation != dict.SituationOther {
		t.Fatalf("update = %+v, %v", f, err)
	}
	if r := reportsByTx(t, svc, f.ID)[e2e("006")]; r.Situation != dict.SituationOther || r.ReportDetails != "Golpe do falso parente" {
		t.Fatalf("report after the update = %+v", r)
	}
	wantKind(t, mustFail(svc.UpdateFundsRecovery(ctx, payerPSP, f.ID, dict.UpdateRecoveryInput{Participant: payerPSP, Situation: "UNKNOWN"})),
		dict.KindRecoveryInvalid)
	wantKind(t, mustFail(svc.UpdateFundsRecovery(ctx, payeePSP, f.ID, dict.UpdateRecoveryInput{Participant: payeePSP})), dict.KindForbidden)
	if got := events(t, svc, finalPSP); !slices.Equal(got, []dict.EventType{dict.EventRecoveryUpdated}) {
		t.Fatalf("events of a counterparty = %v", got)
	}

	// analysed once the last report is closed
	closeReport(t, svc, reports[e2e("001")], dict.AnalysisAgreed)
	closeReport(t, svc, reports[e2e("002")], dict.AnalysisAgreed)
	closeReport(t, svc, reports[e2e("003")], dict.AnalysisDisagreed)
	if f, _ = svc.GetFundsRecovery(ctx, payerPSP, f.ID); f.Status != dict.RecoveryAwaitingAnalysis {
		t.Fatalf("status with a report left = %s", f.Status)
	}
	closeReport(t, svc, reports[e2e("006")], dict.AnalysisAgreed)
	if f, _ = svc.GetFundsRecovery(ctx, payerPSP, f.ID); f.Status != dict.RecoveryAnalysed {
		t.Fatalf("status after the last report = %s", f.Status)
	}
	wantKind(t, mustFail(svc.UpdateFundsRecovery(ctx, payerPSP, f.ID, dict.UpdateRecoveryInput{Participant: payerPSP})), dict.KindRecoveryOperation)
	// its agreed report does not open the way to a refund of its own
	wantKind(t, mustFail(svc.CreateRefund(ctx, payerPSP, dict.CreateRefundInput{
		Participant: payerPSP, TransactionID: e2e("001"), Reason: dict.RefundFraud, Amount: 100,
	})), dict.KindBadRequest)

	// refunds of what was agreed
	f, err = svc.RefundFundsRecovery(ctx, payerPSP, payerPSP, f.ID)
	if err != nil || f.Status != dict.RecoveryRefunding {
		t.Fatalf("refund = %+v, %v", f, err)
	}
	wantKind(t, mustFail(svc.RefundFundsRecovery(ctx, payerPSP, payerPSP, f.ID)), dict.KindRecoveryOperation)
	refunds, err := svc.FundsRecoveryRefunds(ctx, payerPSP, f.ID)
	if err != nil || len(refunds) != 3 {
		t.Fatalf("refunds = %+v, %v", refunds, err)
	}
	for _, r := range refunds {
		if r.Reason != dict.RefundFraud || r.FundsRecoveryID != f.ID || r.InfractionReportID == "" || r.Status != dict.RefundOpen {
			t.Fatalf("refund = %+v", r)
		}
		if r.TransactionID == e2e("006") && (r.Amount != 50000 || r.Requesting != thirdPSP || r.Contested != finalPSP) {
			t.Fatalf("refund of the third hop = %+v", r)
		}
	}
	for _, r := range refunds {
		if r.TransactionID == e2e("001") {
			wantKind(t, mustFail(svc.CancelRefund(ctx, payerPSP, payerPSP, r.ID)), dict.KindRefundOperation)
		}
	}

	// completed once the last refund is closed
	for i, r := range refunds {
		in := dict.CloseRefundInput{Participant: r.Contested, Result: dict.RefundTotallyAccepted, RefundTransactionID: "D8765432120261120110000000000001"}
		if i == 1 {
			in = dict.CloseRefundInput{Participant: r.Contested, Result: dict.RefundRejected, RejectionReason: dict.RejectNoBalance}
		}
		if _, err := svc.CloseRefund(ctx, r.Contested, r.ID, in); err != nil {
			t.Fatal(err)
		}
	}
	if f, _ = svc.GetFundsRecovery(ctx, payerPSP, f.ID); f.Status != dict.RecoveryCompleted {
		t.Fatalf("status after the last refund = %s", f.Status)
	}
	wantKind(t, mustFail(svc.CancelFundsRecovery(ctx, payerPSP, payerPSP, f.ID)), dict.KindRecoveryOperation)
	if got := events(t, svc, payerPSP); !slices.Equal(got, []dict.EventType{dict.EventRecoveryAnalysed, dict.EventRecoveryCompleted}) {
		t.Fatalf("events of the creator = %v", got)
	}

	// one funds recovery per root, ever
	wantKind(t, mustFail(svc.CreateFundsRecovery(ctx, payerPSP, recoveryInput())), dict.KindRecoveryExists)
}

func TestCreateFundsRecoveryRefused(t *testing.T) {
	ctx := context.Background()
	svc := chain(t)
	cases := []struct {
		name   string
		caller string
		change func(*dict.CreateRecoveryInput)
		kind   dict.Kind
	}{
		{"no contact", payerPSP, func(in *dict.CreateRecoveryInput) { in.Contact = nil }, dict.KindRecoveryInvalid},
		{"interactive flow", payerPSP, func(in *dict.CreateRecoveryInput) { in.FlowType = "INTERACTIVE" }, dict.KindRecoveryInvalid},
		{"OTHER without details", payerPSP, func(in *dict.CreateRecoveryInput) { in.Situation, in.ReportDetails = dict.SituationOther, "" }, dict.KindRecoveryInvalid},
		{"bad root", payerPSP, func(in *dict.CreateRecoveryInput) { in.RootTransactionID = "E123" }, dict.KindRecoveryInvalid},
		{"for another participant", payeePSP, func(*dict.CreateRecoveryInput) {}, dict.KindForbidden},
		{"by the payee", payeePSP, func(in *dict.CreateRecoveryInput) { in.Participant = payeePSP }, dict.KindForbidden},
		{"unknown root", payerPSP, func(in *dict.CreateRecoveryInput) { in.RootTransactionID = e2e("999") }, dict.KindRecoveryNotIndexed},
		{"root below the minimum", payerPSP, func(in *dict.CreateRecoveryInput) { in.Graph.MinAmount = ptr(int64(100001)) }, dict.KindRecoveryNotIndexed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := recoveryInput()
			c.change(&in)
			wantKind(t, mustFail(svc.CreateFundsRecovery(ctx, c.caller, in)), c.kind)
		})
	}

	in := recoveryInput()
	in.Graph = dict.GraphInput{MaxHops: ptr(11), HopWindow: ptr(30 * time.Minute), MaxTransactions: ptr(0), MinAmount: ptr(int64(-1))}
	de := wantKind(t, mustFail(svc.CreateFundsRecovery(ctx, payerPSP, in)), dict.KindRecoveryGraphInvalid)
	if len(de.Violations) != 4 || de.Violations[3] != (dict.Violation{
		Property: "MaxHops", Value: "11", Min: "1", Max: "10", Reason: "Value must be between 1 and 10",
	}) || de.Violations[2].Min != "PT1H" || de.Violations[2].Max != "PT168H" {
		t.Fatalf("violations = %+v", de.Violations)
	}

	// a root already reported
	r := reportInput()
	r.TransactionID = e2e("001")
	if _, err := svc.CreateInfractionReport(ctx, payerPSP, r); err != nil {
		t.Fatal(err)
	}
	wantKind(t, mustFail(svc.CreateFundsRecovery(ctx, payerPSP, recoveryInput())), dict.KindRecoveryNotEligible)
}

// The graph stops at MaxHops and MaxTransactions, and a transaction already reported is tracked but not reported again.
func TestTrackingGraphBounds(t *testing.T) {
	ctx := context.Background()
	svc := chain(t)
	r := reportInput()
	r.Participant, r.TransactionID = payeePSP, e2e("003")
	if _, err := svc.CreateInfractionReport(ctx, payeePSP, r); err != nil {
		t.Fatal(err)
	}
	in := recoveryInput()
	in.Graph.MaxHops = ptr(2)
	f, err := svc.CreateFundsRecovery(ctx, payerPSP, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Graph.Transactions) != 3 {
		t.Fatalf("graph of 2 hops = %+v", f.Graph.Transactions)
	}
	if reports := reportsByTx(t, svc, f.ID); len(reports) != 2 || reports[e2e("003")].ID != "" {
		t.Fatalf("reports = %+v", reports)
	}

	svc = chain(t)
	in.Graph = dict.GraphInput{MaxTransactions: ptr(2)}
	if f, err = svc.CreateFundsRecovery(ctx, payerPSP, in); err != nil || len(f.Graph.Transactions) != 2 {
		t.Fatalf("graph of 2 transactions = %+v, %v", f.Graph.Transactions, err)
	}
}

func TestCancelFundsRecovery(t *testing.T) {
	ctx := context.Background()
	svc := chain(t)
	f, err := svc.CreateFundsRecovery(ctx, payerPSP, recoveryInput())
	if err != nil {
		t.Fatal(err)
	}
	agreed := closeReport(t, svc, reportsByTx(t, svc, f.ID)[e2e("001")], dict.AnalysisAgreed)
	wantKind(t, mustFail(svc.CancelFundsRecovery(ctx, payeePSP, payeePSP, f.ID)), dict.KindForbidden)
	wantKind(t, mustFail(svc.CancelFundsRecovery(ctx, payerPSP, payeePSP, f.ID)), dict.KindForbidden)
	wantKind(t, mustFail(svc.CancelFundsRecovery(ctx, payerPSP, payerPSP, "nope")), dict.KindNotFound)

	f, err = svc.CancelFundsRecovery(ctx, payerPSP, payerPSP, f.ID)
	if err != nil || f.Status != dict.RecoveryCancelled {
		t.Fatalf("cancel = %+v, %v", f, err)
	}
	for id, r := range reportsByTx(t, svc, f.ID) {
		if r.Status != dict.ReportCancelled {
			t.Fatalf("report of %s = %s", id, r.Status)
		}
	}
	if m, err := svc.GetFraudMarker(ctx, payeePSP, agreed.FraudMarkerID); err != nil || m.Status != dict.MarkerCancelled {
		t.Fatalf("marker = %+v, %v", m, err)
	}
	if again, err := svc.CancelFundsRecovery(ctx, payerPSP, payerPSP, f.ID); err != nil || !again.Modified.Equal(f.Modified) {
		t.Fatalf("cancel again = %+v, %v", again, err)
	}
	for _, p := range []string{payeePSP, thirdPSP, finalPSP} {
		if got := events(t, svc, p); !slices.Equal(got, []dict.EventType{dict.EventRecoveryCancelled}) {
			t.Fatalf("events of %s = %v", p, got)
		}
	}
	if got := events(t, svc, payerPSP); len(got) != 0 {
		t.Fatalf("the creator was told of its own cancellation: %v", got)
	}
	wantKind(t, mustFail(svc.CreateFundsRecovery(ctx, payerPSP, recoveryInput())), dict.KindRecoveryExists)
}

// With nothing agreed, asking for the refunds completes the funds recovery at once.
func TestRefundFundsRecoveryNothingAgreed(t *testing.T) {
	ctx := context.Background()
	svc := chain(t)
	in := recoveryInput()
	in.Graph.MaxHops = ptr(1)
	f, err := svc.CreateFundsRecovery(ctx, payerPSP, in)
	if err != nil {
		t.Fatal(err)
	}
	closeReport(t, svc, reportsByTx(t, svc, f.ID)[e2e("001")], dict.AnalysisDisagreed)
	if f, err = svc.RefundFundsRecovery(ctx, payerPSP, payerPSP, f.ID); err != nil || f.Status != dict.RecoveryCompleted {
		t.Fatalf("refund = %+v, %v", f, err)
	}
	if refunds, err := svc.FundsRecoveryRefunds(ctx, payerPSP, f.ID); err != nil || len(refunds) != 0 {
		t.Fatalf("refunds = %+v, %v", refunds, err)
	}
}

func TestDeprecatedRecoveryOperations(t *testing.T) {
	wantKind(t, newService().DeprecatedOperation(), dict.KindDeprecated)
}

func TestEventNotifications(t *testing.T) {
	ctx := context.Background()
	svc := chain(t)
	in := recoveryInput()
	in.Graph.MaxHops = ptr(1) // payeePSP is the only counterparty
	f, err := svc.CreateFundsRecovery(ctx, payerPSP, in)
	if err != nil {
		t.Fatal(err)
	}
	for range 201 {
		if _, err := svc.UpdateFundsRecovery(ctx, payerPSP, f.ID, dict.UpdateRecoveryInput{Participant: payerPSP}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := svc.ListEventNotifications(ctx, payeePSP, dict.ListEventsInput{Participant: payeePSP})
	if err != nil || len(page.Events) != 200 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("first page = %d events, more %v, %v", len(page.Events), page.HasMore, err)
	}
	if e := page.Events[0]; e.EntityID != f.ID || e.Participant != payeePSP || e.Type != dict.EventRecoveryUpdated {
		t.Fatalf("event = %+v", e)
	}
	last, err := svc.ListEventNotifications(ctx, payeePSP, dict.ListEventsInput{Participant: payeePSP, Cursor: page.NextCursor})
	if err != nil || len(last.Events) != 1 || last.HasMore || last.Events[0].ID != page.Events[199].ID+1 {
		t.Fatalf("last page = %+v, %v", last, err)
	}
	wantKind(t, mustFail(svc.ListEventNotifications(ctx, payeePSP, dict.ListEventsInput{Participant: payeePSP, Cursor: "%%"})), dict.KindBadRequest)
	wantKind(t, mustFail(svc.ListEventNotifications(ctx, payeePSP, dict.ListEventsInput{Participant: payerPSP})), dict.KindForbidden)

	// seven days later they are gone
	svc.SetClock(func() time.Time { return t0.Add(48*time.Hour + 7*24*time.Hour + time.Second) })
	if got := events(t, svc, payeePSP); len(got) != 0 {
		t.Fatalf("events older than 7 days = %d", len(got))
	}
}

func TestISODuration(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"PT24H": 24 * time.Hour, "P1D": 24 * time.Hour, "P1DT12H": 36 * time.Hour, "PT90M": 90 * time.Minute, "PT1H30M15S": time.Hour + 30*time.Minute + 15*time.Second,
	} {
		if got, err := dict.ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "P", "PT", "24H", "PT-1H", "P1W", "P1M", "PT1.5H", "P99999999999999999999D"} {
		if _, err := dict.ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) was accepted", in)
		}
	}
	for d, want := range map[time.Duration]string{24 * time.Hour: "PT24H", 90 * time.Minute: "PT1H30M", 15 * time.Second: "PT15S", 0: "PT0S"} {
		if got := dict.FormatDuration(d); got != want {
			t.Errorf("FormatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}
