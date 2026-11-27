package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"pixfake/mock/dict/internal/dict"
)

var recoveryContact = &dict.Contact{Email: "fraude@banco.example.com", Phone: "+5561988887777"}

// fanOut is a root from the payer to a mule, who passes R$ 20,00 on to each of n other users at payeePSP.
func fanOut(t *testing.T, svc *dict.Service, n int) {
	t.Helper()
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	root := medTransaction()
	root.SettlementTime = at
	if err := svc.AddTransaction(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		if err := svc.AddTransaction(context.Background(), dict.Transaction{
			ID: fmt.Sprintf("E87654321202611201000000000%05d", i), DebtorParticipant: payeePSP, DebtorTaxID: root.CreditorTaxID,
			CreditorParticipant: payeePSP, CreditorTaxID: fmt.Sprintf("%011d", i+1), Amount: 2000, SettlementTime: at.Add(time.Duration(i+1) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func createRecovery(t *testing.T, svc *dict.Service) dict.FundsRecovery {
	t.Helper()
	f, err := svc.CreateFundsRecovery(context.Background(), payerPSP, dict.CreateRecoveryInput{
		Participant: payerPSP, RootTransactionID: medTx, Situation: dict.SituationScam, ReportDetails: "QR Code falso", Contact: recoveryContact,
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A funds recovery on PostgreSQL, through the service: every record is written, read back and changed.
func TestRepositoryFundsRecovery(t *testing.T) {
	ctx := context.Background()
	repo := newRepo(t)
	svc := dict.NewService(repo)
	fanOut(t, svc, 2)

	f := createRecovery(t, svc)
	got, err := svc.GetFundsRecovery(ctx, payeePSP, f.ID)
	if err != nil || !reflect.DeepEqual(got, f) {
		t.Fatalf("read back = %+v, %v\nwant %+v", got, err, f)
	}
	if len(f.Graph.Transactions) != 3 || f.Graph.Transactions[2].Depth != 2 || f.Graph.Parameters != dict.DefaultGraphParameters {
		t.Fatalf("graph = %+v", f.Graph)
	}
	wantErr := func(err error, kind dict.Kind) {
		t.Helper()
		var de *dict.Error
		if !errors.As(err, &de) || de.Kind != kind {
			t.Fatalf("err = %v, want %s", err, kind)
		}
	}
	_, err = svc.CreateFundsRecovery(ctx, payerPSP, dict.CreateRecoveryInput{
		Participant: payerPSP, RootTransactionID: medTx, Situation: dict.SituationScam, Contact: recoveryContact,
	})
	wantErr(err, dict.KindRecoveryExists)
	_, err = svc.GetFundsRecovery(ctx, payeePSP, "not-a-uuid")
	wantErr(err, dict.KindNotFound)

	if f, err = svc.UpdateFundsRecovery(ctx, payerPSP, f.ID, dict.UpdateRecoveryInput{
		Participant: payerPSP, Contact: &dict.Contact{Email: "outro@banco.example.com", Phone: "+5561977776666"},
	}); err != nil || f.Contact.Email != "outro@banco.example.com" {
		t.Fatalf("update = %+v, %v", f, err)
	}
	reports, err := svc.FundsRecoveryReports(ctx, payerPSP, f.ID)
	if err != nil || len(reports) != 3 {
		t.Fatalf("reports = %+v, %v", reports, err)
	}
	for _, r := range reports {
		if r.FundsRecoveryID != f.ID || r.Depth == 0 || r.Contact.Email != "outro@banco.example.com" {
			t.Fatalf("report = %+v", r)
		}
		if _, err := svc.AcknowledgeInfractionReport(ctx, r.Counterparty, r.Counterparty, r.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.CloseInfractionReport(ctx, r.Counterparty, r.ID, dict.CloseReportInput{
			Participant: r.Counterparty, Result: dict.AnalysisAgreed, FraudType: dict.FraudMule,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if f, err = svc.RefundFundsRecovery(ctx, payerPSP, payerPSP, f.ID); err != nil || f.Status != dict.RecoveryRefunding {
		t.Fatalf("refund = %+v, %v", f, err)
	}
	refunds, err := svc.FundsRecoveryRefunds(ctx, payerPSP, f.ID)
	if err != nil || len(refunds) != 3 {
		t.Fatalf("refunds = %+v, %v", refunds, err)
	}
	for _, r := range refunds {
		if r.FundsRecoveryID != f.ID {
			t.Fatalf("refund = %+v", r)
		}
		if _, err := svc.CloseRefund(ctx, r.Contested, r.ID, dict.CloseRefundInput{
			Participant: r.Contested, Result: dict.RefundRejected, RejectionReason: dict.RejectNoBalance,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if f, _ = svc.GetFundsRecovery(ctx, payerPSP, f.ID); f.Status != dict.RecoveryCompleted {
		t.Fatalf("status = %s", f.Status)
	}

	page, err := svc.ListEventNotifications(ctx, payerPSP, dict.ListEventsInput{Participant: payerPSP})
	if err != nil || len(page.Events) != 2 || page.Events[0].Type != dict.EventRecoveryAnalysed || page.Events[1].Type != dict.EventRecoveryCompleted ||
		page.Events[0].EntityID != f.ID {
		t.Fatalf("events = %+v, %v", page, err)
	}
	if later, _ := repo.ListEvents(ctx, payerPSP, page.Events[0].ID, time.Time{}, 10); len(later) != 1 {
		t.Fatalf("events after the first = %+v", later)
	}
	if recent, _ := repo.ListEvents(ctx, payerPSP, 0, time.Now().Add(time.Hour), 10); len(recent) != 0 {
		t.Fatalf("events since a later time = %+v", recent)
	}

	if err := repo.DeleteAll(ctx); err != nil {
		t.Fatal(err)
	}
	if events, _ := repo.ListEvents(ctx, payerPSP, 0, time.Time{}, 10); len(events) != 0 {
		t.Fatalf("events after DeleteAll = %+v", events)
	}
}

// The counterparties closing every report at once: the funds recovery is analysed once, by whichever closes the last.
// With the creator cancelling at the same time, it ends either cancelled with every report, or analysed.
func TestRepositoryFundsRecoveryConcurrency(t *testing.T) {
	ctx := context.Background()
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", cancel), func(t *testing.T) {
			repo := newRepo(t)
			svc := dict.NewService(repo)
			fanOut(t, svc, 6)
			f := createRecovery(t, svc)
			reports, err := svc.FundsRecoveryReports(ctx, payerPSP, f.ID)
			if err != nil || len(reports) != 7 {
				t.Fatalf("reports = %d, %v", len(reports), err)
			}
			for _, r := range reports {
				if _, err := svc.AcknowledgeInfractionReport(ctx, r.Counterparty, r.Counterparty, r.ID); err != nil {
					t.Fatal(err)
				}
			}
			var wg sync.WaitGroup
			errs := make([]error, len(reports))
			for i, r := range reports {
				wg.Go(func() {
					_, errs[i] = svc.CloseInfractionReport(ctx, r.Counterparty, r.ID, dict.CloseReportInput{
						Participant: r.Counterparty, Result: dict.AnalysisDisagreed,
					})
				})
			}
			var cancelErr error
			if cancel {
				wg.Go(func() { _, cancelErr = svc.CancelFundsRecovery(ctx, payerPSP, payerPSP, f.ID) })
			}
			wg.Wait()
			if cancelErr != nil {
				t.Fatalf("cancel = %v", cancelErr)
			}
			for _, err := range errs {
				var de *dict.Error
				if err != nil && !(cancel && errors.As(err, &de) && de.Kind == dict.KindConflict) {
					t.Fatalf("close = %v", err)
				}
			}
			final, err := svc.GetFundsRecovery(ctx, payerPSP, f.ID)
			if err != nil {
				t.Fatal(err)
			}
			analysed := 0
			events, _ := repo.ListEvents(ctx, payerPSP, 0, time.Time{}, 100)
			for _, e := range events {
				if e.Type == dict.EventRecoveryAnalysed {
					analysed++
				}
			}
			switch {
			case final.Status == dict.RecoveryAnalysed && analysed == 1:
			case cancel && final.Status == dict.RecoveryCancelled && analysed <= 1:
				after, _ := svc.FundsRecoveryReports(ctx, payerPSP, f.ID)
				for _, r := range after {
					if r.Status != dict.ReportCancelled {
						t.Fatalf("a report of the cancelled funds recovery is %s", r.Status)
					}
				}
			default:
				t.Fatalf("status %s with %d ANALYSED events", final.Status, analysed)
			}
		})
	}
}
