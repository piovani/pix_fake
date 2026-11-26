package dict_test

import (
	"context"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
)

const (
	payerPSP = psp      // the payer's participant: reports and asks for refunds
	payeePSP = otherPSP // the payee's participant: acknowledges, closes, refunds
	thirdPSP = "11223344"
	payerTax = "11122233300"
	payeeTax = "39053344705"
	payeeKey = "+5521988887777"
	txID     = "E1234567820261120100000000000001"
)

// medService has the payee's key registered and one settled transaction from the payer to it, of R$ 150,00.
func medService(t *testing.T) *dict.Service {
	t.Helper()
	ctx := context.Background()
	svc := newService()
	e := phoneEntry()
	e.Key, e.Account.Participant, e.Owner.TaxID, e.Owner.Name = payeeKey, payeePSP, payeeTax, "Carlos Mendes"
	if _, err := svc.CreateEntry(ctx, payeePSP, createInput(e)); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddTransaction(ctx, dict.Transaction{
		ID: txID, DebtorParticipant: payerPSP, DebtorTaxID: payerTax, CreditorParticipant: payeePSP, CreditorTaxID: payeeTax,
		CreditorKey: payeeKey, Amount: 15000,
	}); err != nil {
		t.Fatal(err)
	}
	return svc
}

func reportInput() dict.CreateReportInput {
	return dict.CreateReportInput{
		Participant: payerPSP, TransactionID: txID, Reason: dict.ReasonRefundRequest, Situation: dict.SituationScam,
		ReportDetails: "QR Code falso", Contact: &dict.Contact{Email: "fraude@banco.example.com", Phone: "+5561988887777"},
	}
}

// agreedReport opens a report, has the payee's participant acknowledge it and agree.
func agreedReport(t *testing.T, svc *dict.Service) dict.InfractionReport {
	t.Helper()
	ctx := context.Background()
	r, err := svc.CreateInfractionReport(ctx, payerPSP, reportInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcknowledgeInfractionReport(ctx, payeePSP, payeePSP, r.ID); err != nil {
		t.Fatal(err)
	}
	r, err = svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{
		Participant: payeePSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudScammer, AnalysisDetails: "Conta bloqueada",
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestAddTransaction(t *testing.T) {
	svc := newService()
	err := svc.AddTransaction(context.Background(), dict.Transaction{ID: "E123", DebtorParticipant: payerPSP, Amount: 0})
	de := wantKind(t, err, dict.KindBadRequest)
	if len(de.Violations) < 4 {
		t.Fatalf("violations = %+v", de.Violations)
	}
}

func TestInfractionReport(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)

	r, err := svc.CreateInfractionReport(ctx, payerPSP, reportInput())
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != dict.ReportOpen || r.Reporter != payerPSP || r.Counterparty != payeePSP || r.Amount != 15000 || r.ID == "" {
		t.Fatalf("report = %+v", r)
	}

	t.Run("one report in progress per transaction", func(t *testing.T) {
		_, err := svc.CreateInfractionReport(ctx, payerPSP, reportInput())
		wantKind(t, err, dict.KindConflict)
	})
	t.Run("only the payer's participant asks for a refund", func(t *testing.T) {
		in := reportInput()
		in.Participant = payeePSP
		_, err := svc.CreateInfractionReport(ctx, payeePSP, in)
		wantKind(t, err, dict.KindForbidden)
	})
	t.Run("for another participant", func(t *testing.T) {
		_, err := svc.CreateInfractionReport(ctx, thirdPSP, reportInput())
		wantKind(t, err, dict.KindForbidden)
	})
	t.Run("a transaction the DICT does not know", func(t *testing.T) {
		in := reportInput()
		in.TransactionID = "E1234567820261120100000000000099"
		_, err := svc.CreateInfractionReport(ctx, payerPSP, in)
		wantKind(t, err, dict.KindReportInvalid)
	})
	t.Run("invalid fields", func(t *testing.T) {
		in := reportInput()
		in.Situation, in.ReportDetails, in.Contact = dict.SituationOther, "", &dict.Contact{Email: "x", Phone: "1"}
		de := wantKind(t, mustFail(svc.CreateInfractionReport(ctx, payerPSP, in)), dict.KindReportInvalid)
		if len(de.Violations) != 3 {
			t.Fatalf("violations = %+v", de.Violations)
		}
		in = reportInput()
		in.Reason = dict.ReasonRefundCancelled
		wantKind(t, mustFail(svc.CreateInfractionReport(ctx, payerPSP, in)), dict.KindReportInvalid)
		in.Situation = dict.SituationUnknown
		wantKind(t, mustFail(svc.CreateInfractionReport(ctx, payerPSP, in)), dict.KindReportInvalid)
	})
	t.Run("a third participant does not see it", func(t *testing.T) {
		_, err := svc.GetInfractionReport(ctx, thirdPSP, r.ID)
		wantKind(t, err, dict.KindNotFound)
		_, err = svc.AcknowledgeInfractionReport(ctx, thirdPSP, thirdPSP, r.ID)
		wantKind(t, err, dict.KindNotFound)
	})
	t.Run("only the counterparty acknowledges and closes", func(t *testing.T) {
		_, err := svc.AcknowledgeInfractionReport(ctx, payerPSP, payerPSP, r.ID)
		wantKind(t, err, dict.KindForbidden)
		_, err = svc.CloseInfractionReport(ctx, payerPSP, r.ID, dict.CloseReportInput{Participant: payerPSP, Result: dict.AnalysisDisagreed})
		wantKind(t, err, dict.KindForbidden)
	})
	t.Run("an open report cannot be closed", func(t *testing.T) {
		_, err := svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{Participant: payeePSP, Result: dict.AnalysisDisagreed})
		wantKind(t, err, dict.KindConflict)
	})
	t.Run("update by the reporter", func(t *testing.T) {
		details := "QR Code falso em boleto"
		got, err := svc.UpdateInfractionReport(ctx, payerPSP, r.ID, dict.UpdateReportInput{
			Participant: payerPSP, Situation: dict.SituationAccountTakeover, ReportDetails: &details,
		})
		if err != nil || got.Situation != dict.SituationAccountTakeover || got.ReportDetails != details || got.Contact == nil {
			t.Fatalf("update = %+v, %v", got, err)
		}
		_, err = svc.UpdateInfractionReport(ctx, payeePSP, r.ID, dict.UpdateReportInput{Participant: payeePSP})
		wantKind(t, err, dict.KindForbidden)
	})

	ack, err := svc.AcknowledgeInfractionReport(ctx, payeePSP, payeePSP, r.ID)
	if err != nil || ack.Status != dict.ReportAcknowledged {
		t.Fatalf("acknowledge = %+v, %v", ack, err)
	}
	again, err := svc.AcknowledgeInfractionReport(ctx, payeePSP, payeePSP, r.ID)
	if err != nil || !again.Modified.Equal(ack.Modified) {
		t.Fatalf("acknowledging again = %+v, %v: it should change nothing", again, err)
	}

	t.Run("AGREED needs a fraud type", func(t *testing.T) {
		_, err := svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{Participant: payeePSP, Result: dict.AnalysisAgreed})
		wantKind(t, err, dict.KindReportInvalid)
		_, err = svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{
			Participant: payeePSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudOther,
		})
		wantKind(t, err, dict.KindReportInvalid) // OTHER needs details
		_, err = svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{
			Participant: payeePSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudUnknown,
		})
		wantKind(t, err, dict.KindReportInvalid)
	})

	closeIn := dict.CloseReportInput{Participant: payeePSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudMule, AnalysisDetails: "Bloqueado"}
	closed, err := svc.CloseInfractionReport(ctx, payeePSP, r.ID, closeIn)
	if err != nil || closed.Status != dict.ReportClosed || closed.FraudMarkerID == "" {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	if again, err := svc.CloseInfractionReport(ctx, payeePSP, r.ID, closeIn); err != nil || again.FraudMarkerID != closed.FraudMarkerID {
		t.Fatalf("closing again the same way = %+v, %v", again, err)
	}
	_, err = svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{Participant: payeePSP, Result: dict.AnalysisDisagreed})
	wantKind(t, err, dict.KindConflict)
	_, err = svc.UpdateInfractionReport(ctx, payerPSP, r.ID, dict.UpdateReportInput{Participant: payerPSP})
	wantKind(t, err, dict.KindConflict)

	// the marker is on the payee, registered by its participant, and both participants of the report see it
	m, err := svc.GetFraudMarker(ctx, payeePSP, closed.FraudMarkerID)
	if err != nil || m.TaxID != payeeTax || m.Key != payeeKey || m.Registrant != payeePSP || m.FraudType != dict.FraudMule ||
		m.ReportID != r.ID || m.Reporter != payerPSP || m.Status != dict.MarkerRegistered {
		t.Fatalf("marker = %+v, %v", m, err)
	}
	if _, err := svc.GetFraudMarker(ctx, payerPSP, m.ID); err != nil {
		t.Fatal(err)
	}
	_, err = svc.GetFraudMarker(ctx, thirdPSP, m.ID)
	wantKind(t, err, dict.KindNotFound)

	// the reporter withdraws the report: the marker goes with it
	cancelled, err := svc.CancelInfractionReport(ctx, payerPSP, payerPSP, r.ID)
	if err != nil || cancelled.Status != dict.ReportCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if m, _ := svc.GetFraudMarker(ctx, payeePSP, m.ID); m.Status != dict.MarkerCancelled {
		t.Fatalf("marker after the report was cancelled = %+v", m)
	}
	if _, err := svc.CancelInfractionReport(ctx, payerPSP, payerPSP, r.ID); err != nil {
		t.Fatalf("cancelling again = %v", err)
	}
	_, err = svc.CancelInfractionReport(ctx, payeePSP, payeePSP, r.ID)
	wantKind(t, err, dict.KindForbidden)

	// a cancelled report no longer blocks a new one
	if _, err := svc.CreateInfractionReport(ctx, payerPSP, reportInput()); err != nil {
		t.Fatal(err)
	}
}

func mustFail[T any](_ T, err error) error { return err }

func TestRefundCancelledReport(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)
	in := reportInput()
	in.Participant, in.Reason, in.Situation, in.ReportDetails = payeePSP, dict.ReasonRefundCancelled, dict.SituationOther, "A devolução foi indevida"
	r, err := svc.CreateInfractionReport(ctx, payeePSP, in)
	if err != nil || r.Reporter != payeePSP || r.Counterparty != payerPSP {
		t.Fatalf("report = %+v, %v", r, err)
	}
	if _, err := svc.AcknowledgeInfractionReport(ctx, payerPSP, payerPSP, r.ID); err != nil {
		t.Fatal(err)
	}
	r, err = svc.CloseInfractionReport(ctx, payerPSP, r.ID, dict.CloseReportInput{Participant: payerPSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudScammer})
	if err != nil {
		t.Fatal(err)
	}
	m, err := svc.GetFraudMarker(ctx, payerPSP, r.FraudMarkerID)
	if err != nil || m.TaxID != payerTax || m.Key != "" {
		t.Fatalf("a REFUND_CANCELLED report is about the payer: marker = %+v, %v", m, err)
	}
}

func TestListInfractionReports(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)
	clock := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { clock = clock.Add(time.Second); return clock })
	first, err := svc.CreateInfractionReport(ctx, payerPSP, reportInput())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcknowledgeInfractionReport(ctx, payeePSP, payeePSP, first.ID); err != nil {
		t.Fatal(err)
	}
	in := reportInput()
	in.Participant, in.Reason, in.Situation = payeePSP, dict.ReasonRefundCancelled, dict.SituationOther
	second, err := svc.CreateInfractionReport(ctx, payeePSP, in)
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	list := func(caller string, in dict.ListReportsInput) []string {
		t.Helper()
		in.Participant = caller
		got, _, err := svc.ListInfractionReports(ctx, caller, in)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if got := list(payeePSP, dict.ListReportsInput{}); len(got) != 2 || got[0] != first.ID {
		t.Fatalf("all = %v", got)
	}
	if got := list(payeePSP, dict.ListReportsInput{IsReporter: &yes}); len(got) != 1 || got[0] != second.ID {
		t.Fatalf("as reporter = %v", got)
	}
	if got := list(payeePSP, dict.ListReportsInput{IsCounterparty: &yes, IsReporter: &no}); len(got) != 1 || got[0] != first.ID {
		t.Fatalf("as counterparty = %v", got)
	}
	if got := list(payeePSP, dict.ListReportsInput{Status: []dict.ReportStatus{dict.ReportOpen}}); len(got) != 1 || got[0] != second.ID {
		t.Fatalf("open = %v", got)
	}
	if got := list(payeePSP, dict.ListReportsInput{ModifiedAfter: second.Modified}); len(got) != 1 || got[0] != second.ID {
		t.Fatalf("modified after = %v", got)
	}
	if got := list(thirdPSP, dict.ListReportsInput{}); len(got) != 0 {
		t.Fatalf("a third participant = %v", got)
	}
	got, more, err := svc.ListInfractionReports(ctx, payeePSP, dict.ListReportsInput{Participant: payeePSP, Limit: 1})
	if err != nil || len(got) != 1 || !more {
		t.Fatalf("limit 1 = %d, %v, %v", len(got), more, err)
	}
	_, _, err = svc.ListInfractionReports(ctx, payeePSP, dict.ListReportsInput{Participant: payerPSP})
	wantKind(t, err, dict.KindForbidden)
	_, _, err = svc.ListInfractionReports(ctx, payeePSP, dict.ListReportsInput{Participant: payeePSP, Limit: 500, Status: []dict.ReportStatus{"DONE"}})
	if de := wantKind(t, err, dict.KindBadRequest); len(de.Violations) != 2 {
		t.Fatalf("violations = %+v", de.Violations)
	}
}

func refundInput(reason dict.RefundReason, amount int64) dict.CreateRefundInput {
	return dict.CreateRefundInput{Participant: payerPSP, TransactionID: txID, Reason: reason, Amount: amount, Details: "Falha no envio"}
}

func TestRefund(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)

	_, err := svc.CreateRefund(ctx, payerPSP, refundInput(dict.RefundFraud, 10000))
	wantKind(t, err, dict.KindBadRequest) // no infraction report agreed yet
	report := agreedReport(t, svc)

	f, err := svc.CreateRefund(ctx, payerPSP, refundInput(dict.RefundFraud, 10000))
	if err != nil || f.Status != dict.RefundOpen || f.InfractionReportID != report.ID || f.Requesting != payerPSP || f.Contested != payeePSP {
		t.Fatalf("refund = %+v, %v", f, err)
	}
	t.Run("one open refund per transaction", func(t *testing.T) {
		_, err := svc.CreateRefund(ctx, payerPSP, refundInput(dict.RefundOperationalFlaw, 100))
		wantKind(t, err, dict.KindConflict)
	})
	t.Run("only the payer's participant asks", func(t *testing.T) {
		in := refundInput(dict.RefundOperationalFlaw, 100)
		in.Participant = payeePSP
		_, err := svc.CreateRefund(ctx, payeePSP, in)
		wantKind(t, err, dict.KindForbidden)
	})
	t.Run("invalid fields", func(t *testing.T) {
		in := refundInput("REFUND_CANCELLED (DEPRECATED)", 0)
		in.TransactionID = "E1"
		if de := wantKind(t, mustFail(svc.CreateRefund(ctx, payerPSP, in)), dict.KindBadRequest); len(de.Violations) != 3 {
			t.Fatalf("violations = %+v", de.Violations)
		}
		in = refundInput(dict.RefundOperationalFlaw, 100)
		in.Details = " "
		wantKind(t, mustFail(svc.CreateRefund(ctx, payerPSP, in)), dict.KindBadRequest)
	})
	t.Run("only the contested participant closes", func(t *testing.T) {
		_, err := svc.CloseRefund(ctx, payerPSP, f.ID, dict.CloseRefundInput{Participant: payerPSP, Result: dict.RefundRejected, RejectionReason: dict.RejectNoBalance})
		wantKind(t, err, dict.KindForbidden)
		_, err = svc.GetRefund(ctx, thirdPSP, f.ID)
		wantKind(t, err, dict.KindNotFound)
	})
	t.Run("an accepted refund needs the return that paid it", func(t *testing.T) {
		_, err := svc.CloseRefund(ctx, payeePSP, f.ID, dict.CloseRefundInput{Participant: payeePSP, Result: dict.RefundTotallyAccepted})
		wantKind(t, err, dict.KindBadRequest)
		_, err = svc.CloseRefund(ctx, payeePSP, f.ID, dict.CloseRefundInput{Participant: payeePSP, Result: dict.RefundRejected, RejectionReason: dict.RejectParticipantExclusion})
		wantKind(t, err, dict.KindBadRequest)
		_, err = svc.CloseRefund(ctx, payeePSP, f.ID, dict.CloseRefundInput{
			Participant: payeePSP, Result: dict.RefundRejected, RejectionReason: dict.RejectInvalidRequest, AnalysisDetails: "Sem falha",
		})
		wantKind(t, err, dict.KindBadRequest) // INVALID_REQUEST is only for OPERATIONAL_FLAW
	})

	closeIn := dict.CloseRefundInput{Participant: payeePSP, Result: dict.RefundTotallyAccepted, RefundTransactionID: "D8765432120261120110000000000001"}
	closed, err := svc.CloseRefund(ctx, payeePSP, f.ID, closeIn)
	if err != nil || closed.Status != dict.RefundClosed || closed.RefundTransactionID != closeIn.RefundTransactionID {
		t.Fatalf("close = %+v, %v", closed, err)
	}
	if _, err := svc.CloseRefund(ctx, payeePSP, f.ID, closeIn); err != nil {
		t.Fatalf("closing again the same way = %v", err)
	}
	_, err = svc.CloseRefund(ctx, payeePSP, f.ID, dict.CloseRefundInput{Participant: payeePSP, Result: dict.RefundRejected, RejectionReason: dict.RejectOther})
	wantKind(t, err, dict.KindConflict)
	_, err = svc.CancelRefund(ctx, payerPSP, payerPSP, f.ID)
	wantKind(t, err, dict.KindConflict)

	// R$ 100,00 of R$ 150,00 came back: at most R$ 50,00 more
	_, err = svc.CreateRefund(ctx, payerPSP, refundInput(dict.RefundOperationalFlaw, 5001))
	wantKind(t, err, dict.KindBadRequest)
	rest, err := svc.CreateRefund(ctx, payerPSP, refundInput(dict.RefundOperationalFlaw, 5000))
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.CancelRefund(ctx, payeePSP, payeePSP, rest.ID)
	wantKind(t, err, dict.KindForbidden)
	if c, err := svc.CancelRefund(ctx, payerPSP, payerPSP, rest.ID); err != nil || c.Status != dict.RefundCancelled {
		t.Fatalf("cancel = %+v, %v", c, err)
	}
	if _, err := svc.CancelRefund(ctx, payerPSP, payerPSP, rest.ID); err != nil {
		t.Fatalf("cancelling again = %v", err)
	}
	_, err = svc.CloseRefund(ctx, payeePSP, rest.ID, closeIn)
	wantKind(t, err, dict.KindConflict)
}

func TestListRefunds(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)
	clock := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { clock = clock.Add(time.Second); return clock })
	var ids []string
	for range 3 {
		f, err := svc.CreateRefund(ctx, payerPSP, refundInput(dict.RefundOperationalFlaw, 100))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CancelRefund(ctx, payerPSP, payerPSP, f.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, f.ID)
	}
	in := dict.ListRefundsInput{Participant: payerPSP, Role: dict.RoleRequesting, Limit: 2}
	page, err := svc.ListRefunds(ctx, payerPSP, in)
	if err != nil || len(page.Refunds) != 2 || !page.HasMore || page.NextCursor == "" || page.Refunds[0].ID != ids[0] {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	in.Cursor = page.NextCursor
	page, err = svc.ListRefunds(ctx, payerPSP, in)
	if err != nil || len(page.Refunds) != 1 || page.HasMore || page.Refunds[0].ID != ids[2] {
		t.Fatalf("second page = %+v, %v", page, err)
	}
	if page, _ := svc.ListRefunds(ctx, payerPSP, dict.ListRefundsInput{Participant: payerPSP, Role: dict.RoleContested}); len(page.Refunds) != 0 {
		t.Fatalf("as contested = %+v", page)
	}
	if page, _ := svc.ListRefunds(ctx, payeePSP, dict.ListRefundsInput{Participant: payeePSP, Role: dict.RoleContested, Status: []dict.RefundStatus{dict.RefundOpen}}); len(page.Refunds) != 0 {
		t.Fatalf("open ones = %+v", page)
	}
	_, err = svc.ListRefunds(ctx, payerPSP, dict.ListRefundsInput{Participant: payerPSP, Cursor: "!!"})
	if de := wantKind(t, err, dict.KindBadRequest); len(de.Violations) != 2 {
		t.Fatalf("violations = %+v", de.Violations) // no role, bad cursor
	}
}

func TestFraudMarker(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)
	in := dict.CreateMarkerInput{Participant: payeePSP, TaxID: payeeTax, Key: payeeKey, FraudType: dict.FraudMule, RequestID: requestID}

	m, err := svc.CreateFraudMarker(ctx, payeePSP, in)
	if err != nil || m.Status != dict.MarkerRegistered || m.Registrant != payeePSP {
		t.Fatalf("marker = %+v, %v", m, err)
	}
	if again, err := svc.CreateFraudMarker(ctx, payeePSP, in); err != nil || again.ID != m.ID {
		t.Fatalf("same RequestId = %+v, %v", again, err)
	}
	other := in
	other.FraudType = dict.FraudScammer
	wantKind(t, mustFail(svc.CreateFraudMarker(ctx, payeePSP, other)), dict.KindRequestIDAlreadyUsed)

	t.Run("a key of someone else", func(t *testing.T) {
		in := in
		in.TaxID, in.RequestID = payerTax, "b946d533-7f22-42a5-9a9b-e87cd55c0f4d"
		wantKind(t, mustFail(svc.CreateFraudMarker(ctx, payeePSP, in)), dict.KindBadRequest)
	})
	t.Run("invalid fields", func(t *testing.T) {
		de := wantKind(t, mustFail(svc.CreateFraudMarker(ctx, payeePSP, dict.CreateMarkerInput{Participant: payeePSP, TaxID: "1", FraudType: dict.FraudUnknown})), dict.KindBadRequest)
		if len(de.Violations) != 3 {
			t.Fatalf("violations = %+v", de.Violations)
		}
	})
	t.Run("for another participant", func(t *testing.T) {
		wantKind(t, mustFail(svc.CreateFraudMarker(ctx, payerPSP, in)), dict.KindForbidden)
	})
	t.Run("another participant does not see it", func(t *testing.T) {
		wantKind(t, mustFail(svc.GetFraudMarker(ctx, payerPSP, m.ID)), dict.KindNotFound)
		wantKind(t, mustFail(svc.CancelFraudMarker(ctx, payerPSP, payerPSP, m.ID)), dict.KindNotFound)
	})

	for i := range 3 {
		in := in
		in.RequestID = []string{
			"c946d533-7f22-42a5-9a9b-e87cd55c0f4d", "d946d533-7f22-42a5-9a9b-e87cd55c0f4d", "e946d533-7f22-42a5-9a9b-e87cd55c0f4d",
		}[i]
		if _, err := svc.CreateFraudMarker(ctx, payeePSP, in); err != nil {
			t.Fatal(err)
		}
	}
	page, err := svc.ListFraudMarkers(ctx, payeePSP, dict.ListMarkersInput{TaxID: payeeTax, Limit: 3, PerPage: 2, Page: 1})
	if err != nil || page.Total != 3 || len(page.Markers) != 1 || page.HasMore {
		t.Fatalf("second page = %+v, %v", page, err)
	}
	if page, _ := svc.ListFraudMarkers(ctx, payerPSP, dict.ListMarkersInput{TaxID: payeeTax}); page.Total != 0 {
		t.Fatalf("markers of another registrant are not listed: %+v", page)
	}
	wantKind(t, mustFail(svc.ListFraudMarkers(ctx, payeePSP, dict.ListMarkersInput{TaxID: payerTax, Key: payeeKey})), dict.KindBadRequest)
	wantKind(t, mustFail(svc.ListFraudMarkers(ctx, payeePSP, dict.ListMarkersInput{Participant: payerPSP, TaxID: payeeTax})), dict.KindForbidden)

	cancelled, err := svc.CancelFraudMarker(ctx, payeePSP, payeePSP, m.ID)
	if err != nil || cancelled.Status != dict.MarkerCancelled {
		t.Fatalf("cancel = %+v, %v", cancelled, err)
	}
	if _, err := svc.CancelFraudMarker(ctx, payeePSP, payeePSP, m.ID); err != nil {
		t.Fatalf("cancelling again = %v", err)
	}
	if page, _ := svc.ListFraudMarkers(ctx, payeePSP, dict.ListMarkersInput{TaxID: payeeTax, Status: dict.MarkerCancelled}); page.Total != 1 {
		t.Fatalf("cancelled ones = %+v", page)
	}

	// a marker from an infraction report can be cancelled by the participant that closed it, not by the reporter
	report := agreedReport(t, svc)
	wantKind(t, mustFail(svc.CancelFraudMarker(ctx, payerPSP, payerPSP, report.FraudMarkerID)), dict.KindForbidden)
	if _, err := svc.CancelFraudMarker(ctx, payeePSP, payeePSP, report.FraudMarkerID); err != nil {
		t.Fatal(err)
	}
}

func TestStatistics(t *testing.T) {
	ctx := context.Background()
	svc := medService(t)
	report := agreedReport(t, svc) // a SCAMMER_ACCOUNT marker on the payee, of R$ 150,00

	in := reportInput()
	in.TransactionID = "E1234567820261120100000000000002"
	if err := svc.AddTransaction(ctx, dict.Transaction{
		ID: in.TransactionID, DebtorParticipant: payerPSP, DebtorTaxID: payerTax, CreditorParticipant: payeePSP,
		CreditorTaxID: payeeTax, CreditorKey: payeeKey, Amount: 9999,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateInfractionReport(ctx, payerPSP, in); err != nil { // open
		t.Fatal(err)
	}
	if _, err := svc.CreateFraudMarker(ctx, payeePSP, dict.CreateMarkerInput{
		Participant: payeePSP, TaxID: payeeTax, FraudType: dict.FraudApplication, RequestID: requestID,
	}); err != nil {
		t.Fatal(err)
	}

	person, err := svc.PersonStatistics(ctx, payeeTax)
	if err != nil {
		t.Fatal(err)
	}
	if person.Settlements.D90 != 2 || person.ScammerAccounts.D90 != 1 || person.ApplicationFrauds.D90 != 1 ||
		person.TotalFraudTransactionAmount.D90 != 150 || person.DistinctFraudReporters.D90 != 1 ||
		person.OpenReports != 1 || person.OpenReportsDistinctReporters != 1 || person.RegisteredAccounts != 1 {
		t.Fatalf("person = %+v", person)
	}
	if person.ScammerAccounts.M12 != 0 {
		t.Fatalf("m12 leaves the current month out: %+v", person.ScammerAccounts)
	}

	owner, key, err := svc.EntryStatistics(ctx, payeeKey)
	if err != nil {
		t.Fatal(err)
	}
	if owner.ScammerAccounts != person.ScammerAccounts || key.ScammerAccounts.D90 != 1 || key.ApplicationFrauds.D90 != 0 ||
		key.Settlements.D90 != 2 || key.DistinctAccounts.D90 != 1 || key.OpenReports != 1 {
		t.Fatalf("owner = %+v\nkey = %+v", owner, key)
	}

	// cancelling the report cancels its marker, which leaves the counters
	if _, err := svc.CancelInfractionReport(ctx, payerPSP, payerPSP, report.ID); err != nil {
		t.Fatal(err)
	}
	if person, _ := svc.PersonStatistics(ctx, payeeTax); person.ScammerAccounts.D90 != 0 || person.TotalFraudTransactionAmount.D90 != 0 {
		t.Fatalf("after cancelling = %+v", person)
	}

	if _, k, err := svc.EntryStatistics(ctx, "unknown@example.com"); err != nil || k.DistinctAccounts.D90 != 0 {
		t.Fatalf("a key that is not registered = %+v, %v", k, err)
	}
	wantKind(t, mustFail(svc.PersonStatistics(ctx, "123")), dict.KindBadRequest)
}

func TestStatisticsWindows(t *testing.T) {
	// the spec's example: on 2023-11-17 at 17:00Z
	d90, m12, m60 := dict.Windows(time.Date(2023, 11, 17, 17, 0, 0, 0, time.UTC))
	for _, c := range []struct {
		name string
		got  time.Time
		want time.Time
	}{
		{"d90 start", d90[0], time.Date(2023, 8, 20, 3, 0, 0, 0, time.UTC)},
		{"m12 start", m12[0], time.Date(2022, 11, 1, 3, 0, 0, 0, time.UTC)},
		{"m12 end", m12[1], time.Date(2023, 11, 1, 3, 0, 0, 0, time.UTC)},
		{"m60 start", m60[0], time.Date(2018, 11, 1, 3, 0, 0, 0, time.UTC)},
		{"m60 end", m60[1], time.Date(2023, 11, 1, 3, 0, 0, 0, time.UTC)},
	} {
		if !c.got.Equal(c.want) {
			t.Errorf("%s = %s, want %s", c.name, c.got.UTC(), c.want)
		}
	}
	if !d90[1].After(time.Date(2023, 11, 17, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("d90 includes the moment itself")
	}
}
