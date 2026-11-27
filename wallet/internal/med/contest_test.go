package med_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"pixfake/wallet/internal/med"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
)

const (
	sentID  = "3b1e2d4c-5a6f-4b7c-8d9e-0f1a2b3c4d5e"
	sentE2E = "E9900000120261127100000000000077"
	contReq = "6f1e2d3c-4b5a-4968-8776-5a4b3c2d1e0f"
)

// The fake DICT as the wallet's counterparty for the payer's side: its reports and refunds are the wallet's.

func (d *fakeDICT) CreateInfractionReport(_ context.Context, r pix.NewReport) (pix.InfractionReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail != nil {
		return pix.InfractionReport{}, d.fail
	}
	for _, x := range d.reports {
		if x.TransactionID == r.TransactionID && (x.Status == "OPEN" || x.Status == "ACKNOWLEDGED") {
			return pix.InfractionReport{}, &pix.APIError{Status: 409, Kind: "Conflict"}
		}
	}
	id := "mine-" + r.TransactionID[len(r.TransactionID)-2:]
	rep := pix.InfractionReport{
		ID: id, TransactionID: r.TransactionID, Reason: "REFUND_REQUEST", SituationType: r.SituationType, Details: r.Details,
		Status: "OPEN", Reporter: "99000001", Counterparty: "99000002", Modified: d.tick(),
	}
	d.reports[id] = rep
	if d.loseAnswer {
		d.loseAnswer = false
		return pix.InfractionReport{}, pix.ErrUnavailable
	}
	return rep, nil
}

func (d *fakeDICT) CancelInfractionReport(_ context.Context, id string) (pix.InfractionReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.reports[id]
	if r.Status == "CLOSED" {
		return r, &pix.APIError{Status: 409, Kind: "InfractionReportOperationInvalid", Detail: "closed"}
	}
	r.Status, r.Modified = "CANCELLED", d.tick()
	d.reports[id] = r
	return r, nil
}

func (d *fakeDICT) CreateRefund(_ context.Context, transactionID, reason string, cents int64, _ string) (pix.Refund, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail != nil {
		return pix.Refund{}, d.fail
	}
	if d.conflictRefund {
		d.conflictRefund = false
		return pix.Refund{}, &pix.APIError{Status: 409, Kind: "Conflict"}
	}
	f := pix.Refund{ID: "refund-" + transactionID[len(transactionID)-2:], TransactionID: transactionID, Reason: reason, AmountCents: cents,
		Status: "OPEN", Requesting: "99000001", Contested: "99000002", Modified: d.tick()}
	d.refunds[f.ID] = f
	return f, nil
}

// contestEnv has the wallet holding a settled payment of 50.00 it sent to Banco Alfa.
func contestEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.payments.sent = map[string]payment.Payment{sentID: {
		ID: sentID, AccountID: account, Direction: payment.Outbound, Type: payment.TypePix, Status: payment.Settled, AmountCents: 5000,
		EndToEndID: sentE2E, FinishedAt: time.Now().Add(-time.Hour),
	}}
	return e
}

func (e *env) contest(t *testing.T) med.Contest {
	t.Helper()
	c, err := e.svc.Contest(context.Background(), med.ContestInput{PaymentID: sentID, Situation: "SCAM", Details: "golpe", RequestID: contReq})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (e *env) syncContests(t *testing.T) int {
	t.Helper()
	n, err := e.svc.SyncContests(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestContestRefunded(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	if c.Status != med.ContestReported || c.ReportID == "" || c.AmountCents != 5000 || c.AccountID != account {
		t.Fatalf("contest = %+v", c)
	}
	if again := e.contest(t); again.ID != c.ID {
		t.Fatalf("the same request made another contest: %+v", again)
	}
	if _, err := e.svc.Contest(ctx, med.ContestInput{PaymentID: sentID, Situation: "COERCION", RequestID: contReq}); !errors.Is(err, med.ErrRequestConflict) {
		t.Fatalf("same request, other data: %v", err)
	}
	if _, err := e.svc.Contest(ctx, med.ContestInput{PaymentID: sentID, Situation: "SCAM", RequestID: "7f1e2d3c-4b5a-4968-8776-5a4b3c2d1e0f"}); !errors.Is(err, med.ErrAlreadyContested) {
		t.Fatalf("a second contest of the payment: %v", err)
	}
	if n := e.syncContests(t); n != 0 {
		t.Fatalf("nothing happened in the DICT, but %d changed", n)
	}

	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "AGREED" })
	if n := e.syncContests(t); n != 1 {
		t.Fatalf("changed %d", n)
	}
	c, _ = e.svc.GetContest(ctx, c.ID)
	if c.Status != med.ContestRefundRequested || c.RefundID == "" {
		t.Fatalf("after the agreement = %+v", c)
	}
	f := e.dict.refunds[c.RefundID]
	if f.AmountCents != 5000 || f.Reason != "FRAUD" || f.TransactionID != sentE2E {
		t.Fatalf("refund asked = %+v", f)
	}

	const rtr = "D99000002202611271000RETURN00009"
	e.payments.received[rtr] = payment.Payment{ID: "r", Type: payment.TypeReturn, Direction: payment.Inbound, Status: payment.Settled, AmountCents: 4000, EndToEndID: rtr}
	f.Status, f.AnalysisResult, f.RefundTransactionID, f.Modified = "CLOSED", "PARTIALLY_ACCEPTED", rtr, e.dict.tick()
	e.dict.refunds[f.ID] = f
	if n := e.syncContests(t); n != 1 {
		t.Fatalf("changed %d", n)
	}
	c, _ = e.svc.GetContest(ctx, c.ID)
	if c.Status != med.ContestRefunded || c.RefundedCents != 4000 || c.ReturnID != rtr {
		t.Fatalf("after the refund = %+v", c)
	}
	if list, err := e.svc.Contests(ctx, account); err != nil || len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("contests = %+v, %v", list, err)
	}
	// Finished: the payment can be contested again for what did not come back... not by the same request.
	if _, err := e.svc.CancelContest(ctx, c.ID); !errors.Is(err, med.ErrNotCancellable) {
		t.Fatalf("cancel a refunded contest: %v", err)
	}
}

func TestContestRejected(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "DISAGREED" })
	e.syncContests(t)
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestRejected || c.Rejection == "" {
		t.Fatalf("contest = %+v", c)
	}
}

func TestContestRefundRejected(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "AGREED" })
	e.syncContests(t)
	c, _ = e.svc.GetContest(ctx, c.ID)
	f := e.dict.refunds[c.RefundID]
	f.Status, f.AnalysisResult, f.RejectionReason, f.Modified = "CLOSED", "REJECTED", "NO_BALANCE", e.dict.tick()
	e.dict.refunds[f.ID] = f
	e.syncContests(t)
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestRejected || c.Rejection != "The account that received the payment has no money left to give back." {
		t.Fatalf("contest = %+v", c)
	}
}

func TestContestWhileTheDICTIsDown(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	e.dict.fail = pix.ErrUnavailable
	c := e.contest(t)
	if c.Status != med.ContestOpening || c.ReportID != "" {
		t.Fatalf("contest = %+v", c)
	}
	e.dict.fail = nil
	if n := e.syncContests(t); n != 0 {
		t.Fatalf("changed %d; the worker leaves a new contest alone for a few seconds, not to race with the call", n)
	}
	backdate(t, e, c.ID)
	if n := e.syncContests(t); n != 1 {
		t.Fatalf("changed %d", n)
	}
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestReported || c.ReportID == "" {
		t.Fatalf("contest = %+v", c)
	}
}

func TestContestWhoseAnswerWasLost(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	e.dict.loseAnswer = true
	c := e.contest(t)
	if c.Status != med.ContestOpening {
		t.Fatalf("contest = %+v", c)
	}
	backdate(t, e, c.ID)
	e.syncContests(t)
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestReported || c.ReportID != "mine-77" {
		t.Fatalf("contest = %+v; want the report the DICT made", c)
	}
}

func TestCancelContest(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	c, err := e.svc.CancelContest(ctx, c.ID)
	if err != nil || c.Status != med.ContestCancelled || e.dict.reports[c.ReportID].Status != "CANCELLED" {
		t.Fatalf("cancel = %+v, %v", c, err)
	}
	if again, err := e.svc.CancelContest(ctx, c.ID); err != nil || again.Status != med.ContestCancelled {
		t.Fatalf("cancel again = %+v, %v", again, err)
	}
	if _, err := e.svc.CancelContest(ctx, "8f1e2d3c-4b5a-4968-8776-5a4b3c2d1e0f"); !errors.Is(err, med.ErrContestNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	// Cancelled, the payment can be contested again.
	if c2, err := e.svc.Contest(ctx, med.ContestInput{PaymentID: sentID, Situation: "SCAM", RequestID: "9f1e2d3c-4b5a-4968-8776-5a4b3c2d1e0f"}); err != nil || c2.ID == c.ID {
		t.Fatalf("contest again = %+v, %v", c2, err)
	}
}

func TestContestRefusals(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	valid := med.ContestInput{PaymentID: sentID, Situation: "SCAM", RequestID: contReq}
	tests := []struct {
		name   string
		mutate func(in *med.ContestInput, p *payment.Payment)
		want   error
	}{
		{"no payment", func(in *med.ContestInput, _ *payment.Payment) { in.PaymentID = "" }, med.ErrInvalid},
		{"bad situation", func(in *med.ContestInput, _ *payment.Payment) { in.Situation = "UNKNOWN" }, med.ErrInvalid},
		{"OTHER without details", func(in *med.ContestInput, _ *payment.Payment) { in.Situation = "OTHER" }, med.ErrInvalid},
		{"bad request id", func(in *med.ContestInput, _ *payment.Payment) { in.RequestID = "x" }, med.ErrInvalid},
		{"unknown payment", func(in *med.ContestInput, _ *payment.Payment) { in.PaymentID = paid + "0" }, payment.ErrPaymentNotFound},
		{"received", func(_ *med.ContestInput, p *payment.Payment) { p.Direction = payment.Inbound }, med.ErrNotContestable},
		{"book transfer", func(_ *med.ContestInput, p *payment.Payment) { p.Type = payment.TypeBookTransfer }, med.ErrNotContestable},
		{"pending", func(_ *med.ContestInput, p *payment.Payment) { p.Status = payment.Pending }, med.ErrNotContestable},
		{"81 days old", func(_ *med.ContestInput, p *payment.Payment) { p.FinishedAt = time.Now().Add(-81 * 24 * time.Hour) }, med.ErrNotContestable},
		{"all returned", func(_ *med.ContestInput, p *payment.Payment) { p.ReturnedCents = p.AmountCents }, med.ErrNotContestable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, p := valid, e.payments.sent[sentID]
			tt.mutate(&in, &p)
			e.payments.sent[sentID] = p
			if _, err := e.svc.Contest(ctx, in); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
		e.payments.sent[sentID] = payment.Payment{
			ID: sentID, AccountID: account, Direction: payment.Outbound, Type: payment.TypePix, Status: payment.Settled, AmountCents: 5000,
			EndToEndID: sentE2E, FinishedAt: time.Now().Add(-time.Hour),
		}
	}
	if list, err := e.svc.Contests(ctx, account); err != nil || len(list) != 0 {
		t.Fatalf("a refused contest was stored: %+v, %v", list, err)
	}
	if _, err := e.svc.Contests(ctx, ""); !errors.Is(err, med.ErrInvalid) {
		t.Fatalf("no account: %v", err)
	}
}

// backdate makes a contest look older than the few seconds the worker waits for.
func backdate(t *testing.T, e *env, id string) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE med_contests SET updated_at = now() - interval '1 minute' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
}

func TestContestRefundAskedAgainAfterAFailure(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "AGREED" })
	e.dict.fail = pix.ErrUnavailable
	if _, err := e.svc.SyncContests(ctx); err == nil {
		t.Fatal("sync with the DICT down did not fail")
	}
	e.dict.fail = nil
	e.syncContests(t) // the report list again: the contest is AGREED now, waiting for its refund
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestRefundRequested && c.Status != med.ContestAgreed {
		t.Fatalf("contest = %+v", c)
	}
	backdate(t, e, c.ID)
	e.syncContests(t)
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestRefundRequested || c.RefundID == "" {
		t.Fatalf("contest = %+v", c)
	}
}

func TestContestRefundWhoseAnswerWasLost(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	// the refund is already open in the DICT, as after a call whose answer was lost
	e.dict.refunds["refund-77"] = pix.Refund{ID: "refund-77", TransactionID: sentE2E, Reason: "FRAUD", AmountCents: 5000, Status: "OPEN",
		Requesting: "99000001", Contested: "99000002", Modified: e.dict.tick()}
	e.dict.conflictRefund = true
	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "AGREED" })
	e.syncContests(t)
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestRefundRequested || c.RefundID != "refund-77" {
		t.Fatalf("contest = %+v", c)
	}
}

func TestContestEndedByTheOtherSide(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status = "CANCELLED" })
	e.syncContests(t)
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestCancelled {
		t.Fatalf("contest after the report was cancelled = %+v", c)
	}

	e2 := contestEnv(t)
	c2 := e2.contest(t)
	e2.dict.setReport(c2.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "AGREED" })
	e2.syncContests(t)
	c2, _ = e2.svc.GetContest(ctx, c2.ID)
	f := e2.dict.refunds[c2.RefundID]
	f.Status, f.Modified = "CANCELLED", e2.dict.tick()
	e2.dict.refunds[f.ID] = f
	e2.syncContests(t)
	if c2, _ = e2.svc.GetContest(ctx, c2.ID); c2.Status != med.ContestCancelled {
		t.Fatalf("contest after the refund was cancelled = %+v", c2)
	}
	if _, err := e2.svc.CancelContest(ctx, c2.ID); err != nil {
		t.Fatalf("cancel a cancelled contest: %v", err)
	}
}

func TestCancelAnalysedContest(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	c := e.contest(t)
	// closed in the DICT, but the wallet has not read it yet
	e.dict.setReport(c.ReportID, func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "DISAGREED" })
	if _, err := e.svc.CancelContest(ctx, c.ID); !errors.Is(err, med.ErrNotCancellable) {
		t.Fatalf("cancel = %v", err)
	}
}

func TestContestReportRefused(t *testing.T) {
	ctx := context.Background()
	e := contestEnv(t)
	e.dict.fail = &pix.APIError{Status: 400, Kind: "InfractionReportInvalid", Detail: "unknown transaction"}
	c := e.contest(t)
	if c.Status != med.ContestRejected || c.Rejection == "" {
		t.Fatalf("contest = %+v", c)
	}
	if c, _ = e.svc.GetContest(ctx, c.ID); c.Status != med.ContestRejected {
		t.Fatalf("stored = %+v", c)
	}
}
