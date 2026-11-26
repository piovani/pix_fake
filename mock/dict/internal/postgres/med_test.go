package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
)

const (
	payerPSP = "12345678"
	payeePSP = "87654321"
	medTx    = "E1234567820261120100000000000001"
)

func medTransaction() dict.Transaction {
	return dict.Transaction{
		ID: medTx, DebtorParticipant: payerPSP, DebtorTaxID: "11122233300", CreditorParticipant: payeePSP,
		CreditorTaxID: "11222333000150", CreditorKey: "pix@bcb.gov.br", Amount: 15000,
		SettlementTime: time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC),
	}
}

// The whole MED flow on PostgreSQL, through the service: every record is written, read back and changed.
func TestRepositoryMED(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := dict.NewService(repo)
	if err := repo.Create(ctx, stamp(t, entry("pix@bcb.gov.br"), reqA), reqA); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddTransaction(ctx, medTransaction()); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddTransaction(ctx, medTransaction()); err != nil { // adding again replaces it
		t.Fatal(err)
	}
	txs, err := svc.Transactions(ctx)
	if err != nil || len(txs) != 1 || txs[0] != medTransaction() {
		t.Fatalf("transactions = %+v, %v", txs, err)
	}

	contact := &dict.Contact{Email: "fraude@banco.example.com", Phone: "+5561988887777"}
	r, err := svc.CreateInfractionReport(ctx, payerPSP, dict.CreateReportInput{
		Participant: payerPSP, TransactionID: medTx, Reason: dict.ReasonRefundRequest, Situation: dict.SituationScam,
		ReportDetails: "QR Code falso", Contact: contact,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.GetInfractionReport(ctx, payeePSP, r.ID)
	if err != nil || got.Contact == nil || *got.Contact != *contact || got.ReportDetails != "QR Code falso" || got.Amount != 15000 ||
		got.CreditorTaxID != "11222333000150" || !got.Created.Equal(r.Created) {
		t.Fatalf("report read back = %+v, %v", got, err)
	}
	if _, err := svc.GetInfractionReport(ctx, payeePSP, "not-a-uuid"); err == nil {
		t.Fatal("an id that is not a UUID was found")
	}
	if _, err := svc.AcknowledgeInfractionReport(ctx, payeePSP, payeePSP, r.ID); err != nil {
		t.Fatal(err)
	}
	r, err = svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{
		Participant: payeePSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudMule, AnalysisDetails: "Bloqueado",
	})
	if err != nil {
		t.Fatal(err)
	}
	m, err := svc.GetFraudMarker(ctx, payerPSP, r.FraudMarkerID)
	if err != nil || m.ReportID != r.ID || m.Reporter != payerPSP || m.Key != "pix@bcb.gov.br" || m.Amount != 15000 || m.RequestID != "" {
		t.Fatalf("marker = %+v, %v", m, err)
	}
	reports, err := repo.ListReports(ctx, dict.ReportQuery{CreditorKey: "pix@bcb.gov.br"})
	if err != nil || len(reports) != 1 || reports[0].FraudMarkerID != m.ID || reports[0].AnalysisResult != dict.AnalysisAgreed {
		t.Fatalf("reports of the key = %+v, %v", reports, err)
	}

	direct, err := svc.CreateFraudMarker(ctx, payeePSP, dict.CreateMarkerInput{
		Participant: payeePSP, TaxID: "11222333000150", FraudType: dict.FraudApplication, RequestID: reqB,
	})
	if err != nil {
		t.Fatal(err)
	}
	if again, err := svc.CreateFraudMarker(ctx, payeePSP, dict.CreateMarkerInput{
		Participant: payeePSP, TaxID: "11222333000150", FraudType: dict.FraudApplication, RequestID: reqB,
	}); err != nil || again.ID != direct.ID {
		t.Fatalf("same RequestId = %+v, %v", again, err)
	}
	if page, err := svc.ListFraudMarkers(ctx, payeePSP, dict.ListMarkersInput{TaxID: "11222333000150"}); err != nil || page.Total != 2 {
		t.Fatalf("markers = %+v, %v", page, err)
	}

	f, err := svc.CreateRefund(ctx, payerPSP, dict.CreateRefundInput{
		Participant: payerPSP, TransactionID: medTx, Reason: dict.RefundFraud, Amount: 15000,
	})
	if err != nil || f.InfractionReportID != r.ID {
		t.Fatalf("refund = %+v, %v", f, err)
	}
	f, err = svc.CloseRefund(ctx, payeePSP, f.ID, dict.CloseRefundInput{
		Participant: payeePSP, Result: dict.RefundTotallyAccepted, RefundTransactionID: "D8765432120261120110000000000001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := svc.GetRefund(ctx, payerPSP, f.ID); err != nil || got != f {
		t.Fatalf("refund read back = %+v, %v; want %+v", got, err, f)
	}
	if page, err := svc.ListRefunds(ctx, payeePSP, dict.ListRefundsInput{Participant: payeePSP, Role: dict.RoleContested}); err != nil || len(page.Refunds) != 1 {
		t.Fatalf("refunds = %+v, %v", page, err)
	}

	st, err := svc.PersonStatistics(ctx, "11222333000150")
	if err != nil || st.MuleAccounts.M60 != 0 || st.MuleAccounts.D90 != 1 || st.ApplicationFrauds.D90 != 1 || st.RegisteredAccounts != 1 {
		t.Fatalf("statistics = %+v, %v", st, err)
	}

	if err := repo.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if txs, _ := svc.Transactions(ctx); len(txs) != 0 {
		t.Fatalf("transactions after DeleteAll = %+v", txs)
	}
}

// What the function of InTx writes is kept only if it returns nil.
func TestRepositoryMEDRollback(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	if err := repo.AddTransaction(ctx, medTransaction()); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := repo.InTx(ctx, func(tx dict.MEDTx) error {
		now := time.Now().UTC().Truncate(time.Millisecond)
		if err := tx.SaveRefund(ctx, dict.Refund{
			ID: "c946d533-7f22-42a5-9a9b-e87cd55c0f4d", TransactionID: medTx, Reason: dict.RefundOperationalFlaw, Amount: 1,
			Status: dict.RefundOpen, Requesting: payerPSP, Contested: payeePSP, Created: now, Modified: now,
		}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if list, _ := repo.ListRefunds(ctx, payerPSP); len(list) != 0 {
		t.Fatalf("a refund of a failed transaction was kept: %+v", list)
	}
}

// Two participants acting on the same report at once go one after the other: the report is acknowledged once, and
// never closed and cancelled from the same state.
func TestRepositoryMEDConcurrency(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := dict.NewService(repo)
	if err := svc.AddTransaction(ctx, medTransaction()); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		r, err := svc.CreateInfractionReport(ctx, payerPSP, dict.CreateReportInput{
			Participant: payerPSP, TransactionID: medTx, Reason: dict.ReasonRefundRequest, Situation: dict.SituationScam,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.AcknowledgeInfractionReport(ctx, payeePSP, payeePSP, r.ID); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var closeErr, cancelErr error
		wg.Go(func() {
			_, closeErr = svc.CloseInfractionReport(ctx, payeePSP, r.ID, dict.CloseReportInput{
				Participant: payeePSP, Result: dict.AnalysisAgreed, FraudType: dict.FraudScammer,
			})
		})
		wg.Go(func() { _, cancelErr = svc.CancelInfractionReport(ctx, payerPSP, payerPSP, r.ID) })
		wg.Wait()
		if cancelErr != nil {
			t.Fatalf("cancel = %v", cancelErr) // a reporter can always cancel
		}
		final, err := svc.GetInfractionReport(ctx, payerPSP, r.ID)
		if err != nil || final.Status != dict.ReportCancelled {
			t.Fatalf("final = %+v, %v", final, err)
		}
		if closeErr == nil { // closed first: its marker was cancelled with the report
			m, err := svc.GetFraudMarker(ctx, payeePSP, final.FraudMarkerID)
			if err != nil || m.Status != dict.MarkerCancelled {
				t.Fatalf("marker of a report closed then cancelled = %+v, %v", m, err)
			}
		}
	}
}
