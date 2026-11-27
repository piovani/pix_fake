package med_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/med"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/platform/db/dbtest"
)

const (
	tx1     = "E9900000220261127100000000000001"
	account = "5c1f3a52-6b7d-4e0a-9c11-2d5a7b3e9f01"
	paid    = "7a2e4b61-1c3d-4e5f-8a9b-0c1d2e3f4a5b"
)

// fakeDICT holds the reports and refunds against the wallet, and records what the wallet did.
type fakeDICT struct {
	mu      sync.Mutex
	clock   time.Time
	reports map[string]pix.InfractionReport
	refunds map[string]pix.Refund
	acks    int
	fail    error
	// loseAnswer makes the next report be created but answered as if the DICT were down
	loseAnswer bool
	// conflictRefund makes the next refund answer that one is already open
	conflictRefund bool
}

func newDICT() *fakeDICT {
	return &fakeDICT{clock: time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC), reports: map[string]pix.InfractionReport{}, refunds: map[string]pix.Refund{}}
}

func (d *fakeDICT) tick() time.Time { d.clock = d.clock.Add(time.Second); return d.clock }

func (d *fakeDICT) report(id, status string, cents int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reports[id] = pix.InfractionReport{
		ID: id, TransactionID: tx1, Reason: "REFUND_REQUEST", SituationType: "SCAM", Status: status, Reporter: "99000002",
		Counterparty: "99000001", AmountCents: cents, Modified: d.tick(),
	}
}

func (d *fakeDICT) setReport(id string, f func(r *pix.InfractionReport)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.reports[id]
	f(&r)
	r.Modified = d.tick()
	d.reports[id] = r
}

func (d *fakeDICT) refund(id, reason string, cents int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.refunds[id] = pix.Refund{ID: id, TransactionID: tx1, Reason: reason, AmountCents: cents, Status: "OPEN", Requesting: "99000002", Contested: "99000001", Modified: d.tick()}
}

func (d *fakeDICT) ListInfractionReports(_ context.Context, p pix.ListReportsParams) ([]pix.InfractionReport, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail != nil {
		return nil, false, d.fail
	}
	var out []pix.InfractionReport
	for _, r := range d.reports {
		if !r.Modified.Before(p.ModifiedAfter) && (!p.AsReporter || r.Reporter == "99000001") &&
			(!p.AsCounterparty || r.Counterparty == "99000001") && (len(p.Statuses) == 0 || slices.Contains(p.Statuses, r.Status)) {
			out = append(out, r)
		}
	}
	return out, false, nil
}

func (d *fakeDICT) AcknowledgeInfractionReport(_ context.Context, id string) (pix.InfractionReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.reports[id]
	d.acks++
	if r.Status == "OPEN" {
		r.Status, r.Modified = "ACKNOWLEDGED", d.tick()
		d.reports[id] = r
	}
	return r, nil
}

func (d *fakeDICT) CloseInfractionReport(_ context.Context, id, result, fraudType, _ string) (pix.InfractionReport, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.reports[id]
	if r.Status != "ACKNOWLEDGED" {
		return r, &pix.APIError{Status: 409, Kind: "InfractionReportOperationInvalid"}
	}
	r.Status, r.AnalysisResult, r.Modified = "CLOSED", result, d.tick()
	d.reports[id] = r
	return r, nil
}

func (d *fakeDICT) ListRefunds(_ context.Context, p pix.ListRefundsParams) (pix.RefundsPage, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var page pix.RefundsPage
	for _, f := range d.refunds {
		mine := f.Requesting == "99000001"
		if p.AsContested {
			mine = f.Contested == "99000001"
		}
		if mine && !f.Modified.Before(p.ModifiedAfter) && (len(p.Statuses) == 0 || slices.Contains(p.Statuses, f.Status)) {
			page.Refunds = append(page.Refunds, f)
		}
	}
	return page, nil
}

func (d *fakeDICT) GetRefund(_ context.Context, id string) (pix.Refund, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.refunds[id], nil
}

func (d *fakeDICT) CloseRefund(_ context.Context, p pix.CloseRefundParams) (pix.Refund, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	f := d.refunds[p.ID]
	f.Status = "CLOSED"
	f.Details = fmt.Sprintf("%s/%s/%s", p.Result, p.RejectionReason, p.RefundTransactionID)
	d.refunds[p.ID] = f
	return f, nil
}

// fakeAccounts is one account's balance with its blocks.
type fakeAccounts struct {
	available int64
	blocks    map[string]ledger.Reservation
}

func (a *fakeAccounts) Block(_ context.Context, accountID string, upTo int64, reference string) (ledger.Reservation, error) {
	for _, b := range a.blocks {
		if b.Reference == reference {
			return b, nil
		}
	}
	cents := min(upTo, a.available)
	if cents == 0 {
		return ledger.Reservation{}, ledger.ErrInsufficientFunds
	}
	a.available -= cents
	b := ledger.Reservation{ID: fmt.Sprintf("0000000%d-0000-4000-8000-000000000000", len(a.blocks)+1), AccountID: accountID, AmountCents: cents, Reference: reference, Status: ledger.Held}
	a.blocks[b.ID] = b
	return b, nil
}

func (a *fakeAccounts) Release(_ context.Context, id string) error {
	b := a.blocks[id]
	if b.Status == ledger.Held {
		a.available += b.AmountCents
		b.Status = ledger.Released
		a.blocks[id] = b
	}
	return nil
}

func (a *fakeAccounts) Balance(context.Context, string) (ledger.Balance, error) {
	return ledger.Balance{AvailableCents: a.available}, nil
}

// fakePayments has the payment the wallet received and makes the returns.
type fakePayments struct {
	accounts *fakeAccounts
	received map[string]payment.Payment
	returns  map[string]payment.Payment
	sent     map[string]payment.Payment // by id
	asked    []payment.ReturnInput
}

func (p *fakePayments) Received(_ context.Context, e2e string) (payment.Payment, bool, error) {
	r, ok := p.received[e2e]
	return r, ok, nil
}

func (p *fakePayments) Get(_ context.Context, id string) (payment.Payment, error) {
	if r, ok := p.returns[id]; ok {
		return r, nil
	}
	if r, ok := p.sent[id]; ok {
		return r, nil
	}
	return payment.Payment{}, payment.ErrPaymentNotFound
}

func (p *fakePayments) Return(_ context.Context, in payment.ReturnInput) (payment.Payment, error) {
	p.asked = append(p.asked, in)
	for _, r := range p.returns {
		if r.RequestID == in.RequestID {
			return r, nil
		}
	}
	if in.FromBlock != "" {
		_ = p.accounts.Release(context.Background(), in.FromBlock)
	}
	if p.accounts.available < in.AmountCents {
		return payment.Payment{}, ledger.ErrInsufficientFunds
	}
	p.accounts.available -= in.AmountCents
	r := payment.Payment{
		ID: fmt.Sprintf("9000000%d-0000-4000-8000-000000000000", len(p.returns)+1), Type: payment.TypeReturn, Status: payment.Pending,
		AmountCents: in.AmountCents, EndToEndID: "D99000001202611271000RETURN0000" + fmt.Sprint(len(p.returns)+1), RequestID: in.RequestID,
		ReturnReason: in.Reason,
	}
	p.returns[r.ID] = r
	return r, nil
}

func (p *fakePayments) settle(status payment.Status) {
	for id, r := range p.returns {
		r.Status = status
		p.returns[id] = r
	}
}

type env struct {
	dict     *fakeDICT
	accounts *fakeAccounts
	payments *fakePayments
	svc      *med.Service
	store    *med.Postgres
	pool     *pgxpool.Pool
}

// newEnv has the wallet holding 100.00 in the account that received the payment tx1 of 30.00.
func newEnv(t *testing.T) *env {
	t.Helper()
	pool := dbtest.NewPool(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, branch, number, holder_name, tax_id) VALUES ($1, '0001', '00123456', 'Ana', '52998224725')`, account); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO payments (id, account_id, direction, type, status, amount_cents, end_to_end_id, counterparty, created_at)
		VALUES ($1, $2, 'INBOUND', 'PIX', 'SETTLED', 3000, $3, '{}', now())`, paid, account, tx1); err != nil {
		t.Fatal(err)
	}
	accounts := &fakeAccounts{available: 10000, blocks: map[string]ledger.Reservation{}}
	payments := &fakePayments{accounts: accounts, returns: map[string]payment.Payment{}, received: map[string]payment.Payment{
		tx1: {ID: paid, AccountID: account, Direction: payment.Inbound, Type: payment.TypePix, Status: payment.Settled, AmountCents: 3000, EndToEndID: tx1},
	}}
	d := newDICT()
	store := med.NewPostgres(pool)
	return &env{dict: d, accounts: accounts, payments: payments, store: store, pool: pool, svc: med.New(d, accounts, payments, store)}
}

func (e *env) sync(t *testing.T) int {
	t.Helper()
	n, err := e.svc.SyncReports(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *env) local(t *testing.T, id string) med.Report {
	t.Helper()
	r, found, err := e.store.Report(context.Background(), id)
	if err != nil || !found {
		t.Fatalf("report %s: %v, found %v", id, err, found)
	}
	return r
}

func TestNewReportIsAcknowledgedAndBlocked(t *testing.T) {
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)

	if n := e.sync(t); n != 1 {
		t.Fatalf("acted on %d", n)
	}
	if got := e.dict.reports["r1"].Status; got != "ACKNOWLEDGED" {
		t.Fatalf("report in the DICT is %s", got)
	}
	r := e.local(t, "r1")
	if r.Status != med.ReportAcknowledged || r.PaymentID != paid || r.AccountID != account || r.BlockedCents != 3000 || r.BlockStatus != med.BlockHeld {
		t.Fatalf("local report = %+v", r)
	}
	if e.accounts.available != 7000 {
		t.Fatalf("available = %d", e.accounts.available)
	}

	// The acknowledgement comes back in the next list: nothing more happens.
	if n := e.sync(t); n != 0 || e.dict.acks != 1 || e.accounts.available != 7000 {
		t.Fatalf("second sync acted on %d, %d acks, available %d", n, e.dict.acks, e.accounts.available)
	}
	if list, err := e.svc.AwaitingAnalysis(context.Background()); err != nil || len(list) != 1 || list[0].ID != "r1" {
		t.Fatalf("awaiting analysis = %+v, %v", list, err)
	}
}

func TestBlockOnlyWhatIsThere(t *testing.T) {
	e := newEnv(t)
	e.accounts.available = 1200
	e.dict.report("r1", "OPEN", 3000)
	e.sync(t)
	if r := e.local(t, "r1"); r.BlockedCents != 1200 || e.accounts.available != 0 {
		t.Fatalf("report = %+v, available %d", r, e.accounts.available)
	}

	e.accounts.available = 0
	e.dict.report("r2", "OPEN", 3000)
	e.sync(t)
	if r := e.local(t, "r2"); r.BlockedCents != 0 || r.BlockStatus != "" || r.Status != med.ReportAcknowledged {
		t.Fatalf("report with nothing to block = %+v", r)
	}
}

func TestReportOnAPaymentNotReceived(t *testing.T) {
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)
	e.dict.setReport("r1", func(r *pix.InfractionReport) { r.TransactionID = "E9900000220261127100000000000099" })
	e.sync(t)
	if r := e.local(t, "r1"); r.PaymentID != "" || r.BlockStatus != "" || r.Status != med.ReportAcknowledged {
		t.Fatalf("report = %+v", r)
	}
}

func TestCancelledReportReleasesTheBlock(t *testing.T) {
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)
	e.sync(t)
	e.dict.setReport("r1", func(r *pix.InfractionReport) { r.Status = "CANCELLED" })
	if n := e.sync(t); n != 1 {
		t.Fatalf("acted on %d", n)
	}
	if r := e.local(t, "r1"); r.Status != med.ReportCancelled || r.BlockStatus != med.BlockReleased || e.accounts.available != 10000 {
		t.Fatalf("report = %+v, available %d", r, e.accounts.available)
	}
}

func TestAnalyse(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)
	e.dict.report("r2", "OPEN", 1000)
	e.sync(t)

	if _, err := e.svc.Analyse(ctx, med.Analysis{ReportID: "r1", Agree: true, FraudType: "NONSENSE"}); !errors.Is(err, med.ErrInvalid) {
		t.Fatalf("bad fraud type: %v", err)
	}
	if _, err := e.svc.Analyse(ctx, med.Analysis{ReportID: "nope", Agree: false}); !errors.Is(err, med.ErrReportNotFound) {
		t.Fatalf("unknown report: %v", err)
	}

	agreed, err := e.svc.Analyse(ctx, med.Analysis{ReportID: "r1", Agree: true, FraudType: "SCAMMER_ACCOUNT", Details: "golpe"})
	if err != nil || agreed.Result != med.Agreed || agreed.BlockStatus != med.BlockHeld {
		t.Fatalf("agree = %+v, %v", agreed, err)
	}
	disagreed, err := e.svc.Analyse(ctx, med.Analysis{ReportID: "r2"})
	if err != nil || disagreed.Result != med.Disagreed || disagreed.BlockStatus != med.BlockReleased {
		t.Fatalf("disagree = %+v, %v", disagreed, err)
	}
	if e.accounts.available != 7000 || e.dict.reports["r1"].AnalysisResult != "AGREED" || e.dict.reports["r2"].AnalysisResult != "DISAGREED" {
		t.Fatalf("available %d, DICT %+v", e.accounts.available, e.dict.reports)
	}
	if _, err := e.svc.Analyse(ctx, med.Analysis{ReportID: "r1"}); !errors.Is(err, med.ErrNotAnalysable) {
		t.Fatalf("analyse twice: %v", err)
	}
	if n := e.sync(t); n != 0 {
		t.Fatalf("the closed reports came back and %d were acted on", n)
	}
}

func TestRefundFromTheBlock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)
	e.sync(t)
	if _, err := e.svc.Analyse(ctx, med.Analysis{ReportID: "r1", Agree: true, FraudType: "MULE_ACCOUNT"}); err != nil {
		t.Fatal(err)
	}

	e.dict.refund("f1", "FRAUD", 3000)
	if n, err := e.svc.SyncRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("sync refunds = %d, %v", n, err)
	}
	if n, _ := e.svc.SyncRefunds(ctx); n != 0 {
		t.Fatalf("the same refund was recorded twice")
	}
	if n, err := e.svc.ProcessRefunds(ctx); err != nil || n != 0 {
		t.Fatalf("process = %d, %v; the return is still in the SPI", n, err)
	}
	if len(e.payments.asked) != 1 {
		t.Fatalf("returns asked = %+v", e.payments.asked)
	}
	in := e.payments.asked[0]
	if in.PaymentID != paid || in.AmountCents != 3000 || in.Reason != "FR01" || in.FromBlock == "" {
		t.Fatalf("return = %+v", in)
	}
	if r := e.local(t, "r1"); r.BlockStatus != med.BlockReturned {
		t.Fatalf("block = %s", r.BlockStatus)
	}
	if n, _ := e.svc.ProcessRefunds(ctx); n != 0 || len(e.payments.asked) != 1 {
		t.Fatalf("pending return: closed %d, asked %d", n, len(e.payments.asked))
	}

	e.payments.settle(payment.Settled)
	if n, err := e.svc.ProcessRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("process after settling = %d, %v", n, err)
	}
	if f := e.dict.refunds["f1"]; f.Status != "CLOSED" || f.Details != "TOTALLY_ACCEPTED//D99000001202611271000RETURN00001" {
		t.Fatalf("refund in the DICT = %+v", f)
	}
	if f, _, _ := e.store.Refund(ctx, "f1"); f.Status != med.RefundClosed || f.Result != "TOTALLY_ACCEPTED" || f.ReturnCents != 3000 {
		t.Fatalf("local refund = %+v", f)
	}
	if e.accounts.available != 7000 {
		t.Fatalf("available = %d", e.accounts.available)
	}
}

func TestRefundPartlyWithoutBlock(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.accounts.available = 1000
	e.dict.refund("f1", "OPERATIONAL_FLAW", 3000)
	if _, err := e.svc.SyncRefunds(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ProcessRefunds(ctx); err != nil {
		t.Fatal(err)
	}
	if in := e.payments.asked[0]; in.AmountCents != 1000 || in.Reason != "BE08" || in.FromBlock != "" {
		t.Fatalf("return = %+v", in)
	}
	e.payments.settle(payment.Settled)
	if _, err := e.svc.ProcessRefunds(ctx); err != nil {
		t.Fatal(err)
	}
	if f := e.dict.refunds["f1"]; f.Details != "PARTIALLY_ACCEPTED//D99000001202611271000RETURN00001" {
		t.Fatalf("refund = %+v", f)
	}
}

func TestRefundWithNothingToReturn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.accounts.available = 0
	e.dict.refund("f1", "FRAUD", 3000)
	if _, err := e.svc.SyncRefunds(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.ProcessRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("process = %d, %v", n, err)
	}
	if f := e.dict.refunds["f1"]; f.Status != "CLOSED" || f.Details != "REJECTED/NO_BALANCE/" || len(e.payments.asked) != 0 {
		t.Fatalf("refund = %+v, returns %+v", f, e.payments.asked)
	}
}

func TestRefundWhoseReturnIsRejected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.dict.refund("f1", "FRAUD", 3000)
	_, _ = e.svc.SyncRefunds(ctx)
	_, _ = e.svc.ProcessRefunds(ctx)
	e.payments.settle(payment.Rejected)
	if n, err := e.svc.ProcessRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("process = %d, %v", n, err)
	}
	if f := e.dict.refunds["f1"]; f.Details != "REJECTED/OTHER/" {
		t.Fatalf("refund = %+v", f)
	}
}

func TestRefundCancelledBeforeTheReturn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.dict.refund("f1", "FRAUD", 3000)
	_, _ = e.svc.SyncRefunds(ctx)
	f := e.dict.refunds["f1"]
	f.Status = "CANCELLED"
	e.dict.refunds["f1"] = f
	if n, err := e.svc.ProcessRefunds(ctx); err != nil || n != 1 || len(e.payments.asked) != 0 {
		t.Fatalf("process = %d, %v, returns %+v", n, err, e.payments.asked)
	}
	if f, _, _ := e.store.Refund(ctx, "f1"); f.Status != med.RefundClosed || f.Result != "CANCELLED" {
		t.Fatalf("local refund = %+v", f)
	}
}

func TestSyncReportsStopsOnDICTError(t *testing.T) {
	e := newEnv(t)
	e.dict.fail = pix.ErrUnavailable
	if _, err := e.svc.SyncReports(context.Background()); !errors.Is(err, pix.ErrUnavailable) {
		t.Fatalf("err = %v", err)
	}
}

func TestReportsTheWalletDoesNotAnswer(t *testing.T) {
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)
	e.dict.setReport("r1", func(r *pix.InfractionReport) { r.Reason = "REFUND_CANCELLED" })
	e.dict.report("r2", "CANCELLED", 3000) // never seen open
	e.dict.report("r3", "CLOSED", 3000)
	if n := e.sync(t); n != 0 || e.dict.acks != 0 || e.accounts.available != 10000 {
		t.Fatalf("acted on %d, %d acks, available %d", n, e.dict.acks, e.accounts.available)
	}
}

func TestReportClosedElsewhere(t *testing.T) {
	e := newEnv(t)
	e.dict.report("r1", "OPEN", 3000)
	e.sync(t)
	e.dict.setReport("r1", func(r *pix.InfractionReport) { r.Status, r.AnalysisResult = "CLOSED", "DISAGREED" })
	if n := e.sync(t); n != 1 {
		t.Fatalf("acted on %d", n)
	}
	if r := e.local(t, "r1"); r.Status != med.ReportClosed || r.Result != med.Disagreed || r.BlockStatus != med.BlockReleased {
		t.Fatalf("report = %+v", r)
	}
}

func TestRefundOfAPaymentNotReceived(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.dict.refund("f1", "FRAUD", 3000)
	f := e.dict.refunds["f1"]
	f.TransactionID = "E9900000220261127100000000000099"
	e.dict.refunds["f1"] = f
	e.dict.refunds["f2"] = pix.Refund{ID: "f2", TransactionID: tx1, Reason: "REFUND_CANCELLED", Status: "OPEN"}
	if n, err := e.svc.SyncRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("sync = %d, %v; want only the FRAUD one", n, err)
	}
	if n, err := e.svc.ProcessRefunds(ctx); err != nil || n != 1 {
		t.Fatalf("process = %d, %v", n, err)
	}
	if got := e.dict.refunds["f1"].Details; got != "REJECTED/OTHER/" {
		t.Fatalf("refund = %s", got)
	}
}
