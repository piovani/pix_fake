package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/platform/db/dbtest"
)

// These tests run the module on a real PostgreSQL (the ledger and the store); only the DICT and the SPI are replaced.
// Without WALLET_TEST_DATABASE_URL they are skipped.

const (
	walletISPB = "99000001"
	anaID      = "0a1b2c3d-0001-4000-8000-000000000001"
	anaTaxID   = "52998224725"
	brunoID    = "0a1b2c3d-0002-4000-8000-000000000002"
	brunoTaxID = "11144477735"
	anaKey     = "ana.souza@example.com" // a key of the wallet, Ana's
	brunoKey   = "+5511999990002"        // a key of the wallet, Bruno's
)

type fakeKeys map[string]string

func (k fakeKeys) AccountOf(_ context.Context, key string) (string, bool, error) {
	id, ok := k[key]
	return id, ok, nil
}

type fakeDict struct {
	entry pix.Entry
	err   error
	asked []pix.GetEntryParams
}

func (d *fakeDict) GetEntry(_ context.Context, p pix.GetEntryParams) (pix.Entry, error) {
	d.asked = append(d.asked, p)
	return d.entry, d.err
}

type fakeBank struct {
	mu     sync.Mutex
	err    error
	bodies [][]byte
}

func (b *fakeBank) Send(_ context.Context, bodies ...[]byte) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return nil, b.err
	}
	b.bodies = append(b.bodies, bodies...)
	return []string{fmt.Sprintf("resource-%d", len(b.bodies))}, nil
}

func (b *fakeBank) sent() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.bodies)
}

type fixture struct {
	svc    *Service
	ledger *ledger.Ledger
	store  *Postgres
	dict   *fakeDict
	bank   *fakeBank
	keys   fakeKeys
	clock  time.Time
	pool   *pgxpool.Pool
}

func (f *fixture) advance(d time.Duration) { f.clock = f.clock.Add(d) }

// stampReservations moves the reservations to the fixture's clock: the database dates them with its own now(), which
// would otherwise be later than the fixed clock and never look old to the sweep.
func (f *fixture) stampReservations(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(context.Background(), `UPDATE reservations SET created_at = $1`, f.clock); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	pool := dbtest.NewPool(t)
	f := &fixture{
		ledger: ledger.New(pool),
		store:  NewPostgres(pool),
		dict: &fakeDict{entry: pix.Entry{
			Key: "bruno@example.com", KeyType: pix.KeyEmail,
			Owner:   pix.Owner{Type: pix.NaturalPerson, TaxIDNumber: "39053344705", Name: "Carla Dias"},
			Account: pix.Account{Participant: "99000002", Branch: "0001", AccountNumber: "00456789", AccountType: pix.AccountChecking},
		}},
		bank:  &fakeBank{},
		keys:  fakeKeys{anaKey: anaID, brunoKey: brunoID},
		clock: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC),
		pool:  pool,
	}
	f.svc = New(f.ledger, f.keys, f.dict, f.bank, f.store, walletISPB)
	f.svc.now = func() time.Time { return f.clock }

	ctx := context.Background()
	if _, err := f.ledger.Open(ctx, ledger.Account{ID: anaID, Branch: "0001", Number: "00123456", HolderName: "Ana Souza", TaxID: anaTaxID}); err != nil {
		t.Fatal(err)
	}
	if err := f.ledger.Credit(ctx, anaID, 100000, "seed"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ledger.Open(ctx, ledger.Account{ID: brunoID, Branch: "0001", Number: "00234567", HolderName: "Bruno Lima", TaxID: brunoTaxID, Type: "SVGS"}); err != nil {
		t.Fatal(err)
	}
	if err := f.ledger.Credit(ctx, brunoID, 50000, "seed"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) resolve(t *testing.T) Resolution {
	t.Helper()
	r, err := f.svc.ResolveKey(context.Background(), anaID, "bruno@example.com")
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (f *fixture) balance(t *testing.T) ledger.Balance {
	t.Helper()
	b, err := f.ledger.Balance(context.Background(), anaID)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (f *fixture) pay(t *testing.T, r Resolution, cents int64) Payment {
	t.Helper()
	p, err := f.svc.Pay(context.Background(), PayInput{ResolutionID: r.ID, AmountCents: cents, Message: "lunch"})
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	return p
}

func TestResolveKey(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)

	if len(f.dict.asked) != 1 {
		t.Fatalf("DICT asked %d times", len(f.dict.asked))
	}
	asked := f.dict.asked[0]
	if asked.Key != "bruno@example.com" || asked.PayerID != anaTaxID || asked.EndToEndID != r.EndToEndID {
		t.Errorf("DICT asked %+v; the resolution carries %s", asked, r.EndToEndID)
	}
	if len(r.EndToEndID) != 32 || !strings.HasPrefix(r.EndToEndID, "E"+walletISPB+"202611211200") {
		t.Errorf("EndToEndId = %q", r.EndToEndID)
	}
	if r.ID == "" || r.AccountID != anaID || !r.ExpiresAt.Equal(f.clock.Add(ResolutionTTL)) {
		t.Errorf("resolution = %+v", r)
	}
	want := Payee{Name: "Carla Dias", TaxID: "39053344705", Participant: "99000002", Branch: "0001", AccountNumber: "00456789", AccountType: pix.AccountChecking, Key: "bruno@example.com"}
	if r.Payee != want {
		t.Errorf("payee = %+v, want %+v", r.Payee, want)
	}
	if got, found, err := f.store.Resolution(context.Background(), r.ID); err != nil || !found || got.Payee != want || got.EndToEndID != r.EndToEndID {
		t.Errorf("stored: %+v, %v, %v", got, found, err)
	}
	if b := f.balance(t); b.AvailableCents != 100000 || b.ReservedCents != 0 {
		t.Errorf("resolving moved money: %+v", b)
	}
}

func TestResolveKeyErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	for name, tt := range map[string]struct {
		dictErr error
		want    error
	}{
		"unknown key":   {pix.ErrNotFound, ErrKeyNotFound},
		"own key":       {pix.ErrBookTransfer, ErrKeyNotRecorded},
		"unavailable":   {pix.ErrUnavailable, pix.ErrUnavailable},
		"rate limiting": {pix.ErrRateLimited, pix.ErrRateLimited},
	} {
		f.dict.err = tt.dictErr
		if _, err := f.svc.ResolveKey(ctx, anaID, "k"); !errors.Is(err, tt.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	f.dict.err = nil
	if _, err := f.svc.ResolveKey(ctx, "", "k"); !errors.Is(err, ErrInvalid) {
		t.Errorf("no account: %v", err)
	}
	if _, err := f.svc.ResolveKey(ctx, anaID, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("no key: %v", err)
	}
	if _, err := f.svc.ResolveKey(ctx, "0a1b2c3d-9999-4000-8000-000000000009", "k"); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Errorf("unknown account: %v", err)
	}
	if n := len(f.dict.asked); n != 4 {
		t.Errorf("the DICT was asked %d times; a request refused before the lookup must not reach it", n)
	}
}

func TestPay(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	p := f.pay(t, r, 2590)

	if p.Status != Pending || p.Direction != Outbound || p.Type != TypePix || p.AmountCents != 2590 || p.EndToEndID != r.EndToEndID || p.ResolutionID != r.ID {
		t.Fatalf("payment = %+v", p)
	}
	if !p.Submitted() || p.ResourceID != "resource-1" || p.ReservationID == "" || p.Message != "lunch" {
		t.Fatalf("payment = %+v", p)
	}
	if b := f.balance(t); b.AvailableCents != 100000-2590 || b.ReservedCents != 2590 {
		t.Errorf("balance = %+v", b)
	}

	if f.bank.sent() != 1 {
		t.Fatalf("sent %d messages", f.bank.sent())
	}
	orders, err := pix.ParsePacs008(f.bank.bodies[0])
	if err != nil || len(orders) != 1 {
		t.Fatalf("%v, %v", orders, err)
	}
	o := orders[0]
	if o.EndToEndID != r.EndToEndID || o.AmountCents != 2590 || o.Key != "bruno@example.com" || o.Message != "lunch" {
		t.Errorf("order = %+v", o)
	}
	if o.Payer.ISPB != walletISPB || o.Payer.Name != "Ana Souza" || o.Payer.TaxID != anaTaxID || o.Payer.Account != "00123456" {
		t.Errorf("payer = %+v", o.Payer)
	}
	if o.Receiver.ISPB != "99000002" || o.Receiver.TaxID != "39053344705" || o.Receiver.Account != "00456789" || o.Receiver.AccountType != pix.AccountChecking {
		t.Errorf("receiver = %+v", o.Receiver)
	}

	got, err := f.svc.Get(context.Background(), p.ID)
	if err != nil || got.Status != Pending || got.Counterparty.Name != "Carla Dias" || !got.Submitted() {
		t.Errorf("Get: %+v, %v", got, err)
	}
}

func TestPayNoFunds(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	ctx := context.Background()

	_, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 100001})
	if !errors.Is(err, ledger.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if f.bank.sent() != 0 || f.balance(t).ReservedCents != 0 {
		t.Error("something was sent or reserved")
	}
	if _, found, _ := f.store.ByResolution(ctx, r.ID); found {
		t.Error("a payment was stored")
	}

	if err := f.ledger.Credit(ctx, anaID, 5000, "deposit"); err != nil {
		t.Fatal(err)
	}
	if p := f.pay(t, r, 100001); p.Status != Pending {
		t.Errorf("payment = %+v", p)
	}
}

func TestPayIdempotent(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	first := f.pay(t, r, 2590)
	again := f.pay(t, r, 2590)

	if again.ID != first.ID || again.ReservationID != first.ReservationID {
		t.Errorf("a repeat made a new payment: %+v and %+v", first, again)
	}
	if f.bank.sent() != 1 {
		t.Errorf("sent %d orders", f.bank.sent())
	}
	if b := f.balance(t); b.ReservedCents != 2590 {
		t.Errorf("reserved twice: %+v", b)
	}

	if _, err := f.svc.Pay(context.Background(), PayInput{ResolutionID: r.ID, AmountCents: 9999, Message: "lunch"}); !errors.Is(err, ErrResolutionConflict) {
		t.Errorf("another amount: %v", err)
	}
	if _, err := f.svc.Pay(context.Background(), PayInput{ResolutionID: r.ID, AmountCents: 2590, Message: "dinner"}); !errors.Is(err, ErrResolutionConflict) {
		t.Errorf("another message: %v", err)
	}
}

func TestPayConcurrent(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)

	var wg sync.WaitGroup
	ids := make([]string, 8)
	for i := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p, err := f.svc.Pay(context.Background(), PayInput{ResolutionID: r.ID, AmountCents: 1000, Message: "x"}); err == nil {
				ids[i] = p.ID
			}
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if id == "" || id != ids[0] {
			t.Fatalf("not one payment for all calls: %v", ids)
		}
	}
	if b := f.balance(t); b.ReservedCents != 1000 || b.AvailableCents != 99000 {
		t.Errorf("balance = %+v", b)
	}
}

func TestPayValidation(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	ctx := context.Background()

	for name, in := range map[string]PayInput{
		"no resolution": {AmountCents: 100},
		"zero amount":   {ResolutionID: r.ID},
		"long message":  {ResolutionID: r.ID, AmountCents: 100, Message: strings.Repeat("a", MaxMessage+1)},
	} {
		if _, err := f.svc.Pay(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: "0a1b2c3d-9999-4000-8000-000000000009", AmountCents: 100}); !errors.Is(err, ErrResolutionNotFound) {
		t.Errorf("unknown resolution: %v", err)
	}
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: "not-a-uuid", AmountCents: 100}); !errors.Is(err, ErrResolutionNotFound) {
		t.Errorf("malformed resolution: %v", err)
	}
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 100, Message: strings.Repeat("é", MaxMessage)}); err != nil {
		t.Errorf("140 characters of two bytes each: %v", err)
	}

	f.advance(ResolutionTTL + time.Second)
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: f.resolve(t).ID, AmountCents: 100}); err != nil {
		t.Fatalf("a fresh resolution: %v", err)
	}
	old := f.resolveAt(t, f.clock.Add(-ResolutionTTL-time.Second))
	if _, err := f.svc.Pay(ctx, PayInput{ResolutionID: old.ID, AmountCents: 100}); !errors.Is(err, ErrResolutionNotFound) {
		t.Errorf("expired resolution: %v", err)
	}
	if got, err := f.svc.Pay(ctx, PayInput{ResolutionID: r.ID, AmountCents: 100, Message: strings.Repeat("é", MaxMessage)}); err != nil || got.AmountCents != 100 {
		t.Errorf("an expired resolution that already paid still answers: %+v, %v", got, err)
	}
}

func (f *fixture) resolveAt(t *testing.T, at time.Time) Resolution {
	t.Helper()
	was := f.clock
	f.clock = at
	defer func() { f.clock = was }()
	return f.resolve(t)
}

func TestPaySPIDown(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	f.bank.err = fmt.Errorf("%w: connection refused", pix.ErrUnavailable)

	p := f.pay(t, r, 2590) // not an error: the payment exists and will be sent
	if p.Status != Pending || p.Submitted() {
		t.Fatalf("payment = %+v", p)
	}
	if b := f.balance(t); b.ReservedCents != 2590 {
		t.Errorf("the money must stay reserved: %+v", b)
	}

	// too soon: the worker leaves it to the call that created it
	if n, err := f.svc.SendPending(context.Background()); err != nil || n != 0 {
		t.Fatalf("sent %d, %v", n, err)
	}

	f.advance(resubmitAfter + time.Second)
	if n, err := f.svc.SendPending(context.Background()); err != nil || n != 0 {
		t.Fatalf("with the SPI still down: sent %d, %v", n, err)
	}
	f.bank.err = nil
	n, err := f.svc.SendPending(context.Background())
	if err != nil || n != 1 || f.bank.sent() != 1 {
		t.Fatalf("sent %d, %v, %d messages", n, err, f.bank.sent())
	}
	got, _ := f.svc.Get(context.Background(), p.ID)
	if !got.Submitted() || got.ResourceID != "resource-1" {
		t.Errorf("payment = %+v", got)
	}
	if n, _ := f.svc.SendPending(context.Background()); n != 0 || f.bank.sent() != 1 {
		t.Errorf("sent again: %d, %d messages", n, f.bank.sent())
	}
}

func TestPayRetrySendsUnsent(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	f.bank.err = pix.ErrUnavailable
	first := f.pay(t, r, 2590)
	f.bank.err = nil

	again := f.pay(t, r, 2590)
	if again.ID != first.ID || !again.Submitted() || f.bank.sent() != 1 {
		t.Errorf("%+v, %d messages", again, f.bank.sent())
	}
}

func TestPaySPIBadRequest(t *testing.T) {
	f := newFixture(t)
	r := f.resolve(t)
	f.bank.err = &pix.SPIError{Status: 400, Kind: "bad-request"}

	p := f.pay(t, r, 2590)
	if p.Status != Rejected || p.RejectCode == "" || p.RejectReason == "" || p.FinishedAt.IsZero() {
		t.Fatalf("payment = %+v", p)
	}
	if b := f.balance(t); b.AvailableCents != 100000 || b.ReservedCents != 0 {
		t.Errorf("the money must be back: %+v", b)
	}
}

func settled(e2e string) pix.TxStatus {
	return pix.TxStatus{InstrID: e2e, EndToEndID: e2e, Status: pix.StatusSettled, SettledAt: time.Date(2026, 11, 21, 12, 0, 1, 0, time.UTC)}
}

func rejected(e2e, code string) pix.TxStatus {
	return pix.TxStatus{InstrID: e2e, EndToEndID: e2e, Status: pix.StatusRejected, ReasonCode: code, Detail: "text of the other bank"}
}

func TestOnStatusSettled(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.pay(t, f.resolve(t), 2590)

	if err := f.svc.OnStatus(ctx, settled(p.EndToEndID)); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(ctx, p.ID)
	if got.Status != Settled || !got.FinishedAt.Equal(time.Date(2026, 11, 21, 12, 0, 1, 0, time.UTC)) || got.RejectCode != "" {
		t.Errorf("payment = %+v", got)
	}
	if b := f.balance(t); b.AvailableCents != 100000-2590 || b.ReservedCents != 0 {
		t.Errorf("balance = %+v", b)
	}

	if err := f.svc.OnStatus(ctx, settled(p.EndToEndID)); err != nil {
		t.Fatal(err)
	}
	if b := f.balance(t); b.AvailableCents != 100000-2590 || b.ReservedCents != 0 {
		t.Errorf("a repeated status moved money: %+v", b)
	}
	if err := f.svc.OnStatus(ctx, rejected(p.EndToEndID, "AB03")); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Settled {
		t.Errorf("status = %s", got.Status)
	}
	if b := f.balance(t); b.AvailableCents != 100000-2590 {
		t.Errorf("balance = %+v", b)
	}
}

func TestOnStatusRejected(t *testing.T) {
	for _, tt := range []struct {
		code  string
		cause pix.Cause
	}{{"AC07", pix.CauseRecipient}, {"AB03", pix.CauseTransient}, {"ZZ99", pix.CauseUnknown}} {
		t.Run(tt.code, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			p := f.pay(t, f.resolve(t), 2590)

			if err := f.svc.OnStatus(ctx, rejected(p.EndToEndID, tt.code)); err != nil {
				t.Fatal(err)
			}
			got, _ := f.svc.Get(ctx, p.ID)
			want := pix.ReasonOf(tt.code)
			if got.Status != Rejected || got.RejectCode != tt.code || got.RejectReason != want.Message || got.FinishedAt.IsZero() {
				t.Errorf("payment = %+v", got)
			}
			if strings.Contains(got.RejectReason, "other bank") {
				t.Error("text of another institution reached the customer")
			}
			if b := f.balance(t); b.AvailableCents != 100000 || b.ReservedCents != 0 {
				t.Errorf("balance = %+v", b)
			}
			if err := f.svc.OnStatus(ctx, rejected(p.EndToEndID, tt.code)); err != nil {
				t.Fatal(err)
			}
			if b := f.balance(t); b.AvailableCents != 100000 {
				t.Errorf("a repeated status moved money: %+v", b)
			}
			if err := f.svc.OnStatus(ctx, settled(p.EndToEndID)); err != nil {
				t.Fatal(err)
			}
			if got, _ := f.svc.Get(ctx, p.ID); got.Status != Rejected {
				t.Errorf("status = %s", got.Status)
			}
		})
	}
}

func TestOnStatusUnknown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.pay(t, f.resolve(t), 2590)

	for name, st := range map[string]pix.TxStatus{
		"another payment":  settled("E99000001202611211200zzzzzzzzzzz"),
		"a book transfer":  settled(f.settledBookTransferID(t)),
		"a return":         {InstrID: "D99000001202611211200abcdefghijk", EndToEndID: p.EndToEndID, Status: pix.StatusSettled},
		"an acceptance":    {InstrID: p.EndToEndID, EndToEndID: p.EndToEndID, Status: pix.StatusAccepted},
		"a credit notice":  {InstrID: p.EndToEndID, EndToEndID: p.EndToEndID, Status: pix.StatusCredit},
		"nothing to match": {},
	} {
		if err := f.svc.OnStatus(ctx, st); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Pending {
		t.Errorf("status = %s", got.Status)
	}
	if b := f.balance(t); b.ReservedCents != 2590 {
		t.Errorf("balance = %+v", b)
	}
}

func TestOnNotification(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.pay(t, f.resolve(t), 2590)

	if err := f.svc.OnNotification(ctx, pix.Notification{Ref: "somebody-else", Reason: "Invalid XML"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Pending {
		t.Fatalf("an unrelated notification closed the payment: %s", got.Status)
	}

	if err := f.svc.OnNotification(ctx, pix.Notification{Ref: p.ResourceID, Reason: "Invalid XML"}); err != nil {
		t.Fatal(err)
	}
	got, _ := f.svc.Get(ctx, p.ID)
	if got.Status != Rejected || got.RejectCode != "admi.002" || got.RejectReason == "" {
		t.Errorf("payment = %+v", got)
	}
	if b := f.balance(t); b.AvailableCents != 100000 || b.ReservedCents != 0 {
		t.Errorf("balance = %+v", b)
	}
}

func message(t *testing.T, st pix.TxStatus) pix.Message {
	t.Helper()
	_, body, err := pix.NewPacs002(walletISPB, st, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return pix.Message{ResourceID: "r", Body: body}
}

func TestHandle(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	p := f.pay(t, f.resolve(t), 2590)

	for name, m := range map[string]pix.Message{
		"not XML":        {Body: []byte("nope")},
		"a pacs.008":     {Body: []byte(`<Envelope><AppHdr><MsgDefIdr>pacs.008.spi.1.16</MsgDefIdr></AppHdr></Envelope>`)},
		"a broken admi":  {Body: []byte(`<Envelope><AppHdr><MsgDefIdr>admi.002.spi.1.5</MsgDefIdr></AppHdr><Document>`)},
		"a broken pacs2": {Body: []byte(`<Envelope><AppHdr><MsgDefIdr>pacs.002.spi.1.18</MsgDefIdr></AppHdr><Document>`)},
	} {
		if err := f.svc.Handle(ctx, m); err != nil {
			t.Errorf("%s must be skipped, not fail the batch: %v", name, err)
		}
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Pending {
		t.Fatalf("status = %s", got.Status)
	}

	if err := f.svc.Handle(ctx, message(t, settled(p.EndToEndID))); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(ctx, p.ID); got.Status != Settled {
		t.Errorf("status = %s", got.Status)
	}
}

type fakeStream struct {
	mu        sync.Mutex
	batches   [][]pix.Message
	pulls     []string // the next path of each Pull
	acks      []string
	pullErr   error
	onDrained func()
}

func (s *fakeStream) Pull(ctx context.Context, next string) ([]pix.Message, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pulls = append(s.pulls, next)
	if s.pullErr != nil {
		err := s.pullErr
		s.pullErr = nil
		return nil, "", err
	}
	if len(s.batches) == 0 {
		if s.onDrained != nil {
			s.onDrained()
		}
		return nil, "/next/empty", nil
	}
	b := s.batches[0]
	s.batches = s.batches[1:]
	return b, fmt.Sprintf("/next/%d", len(s.pulls)), nil
}

func (s *fakeStream) Ack(_ context.Context, next string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acks = append(s.acks, next)
	return nil
}

func TestConsume(t *testing.T) {
	f := newFixture(t)
	p := f.pay(t, f.resolve(t), 2590)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := &fakeStream{
		batches:   [][]pix.Message{{message(t, settled(p.EndToEndID))}},
		pullErr:   pix.ErrStreamGone,
		onDrained: cancel,
	}
	if err := f.svc.Consume(ctx, stream); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.svc.Get(context.Background(), p.ID); got.Status != Settled {
		t.Errorf("status = %s", got.Status)
	}
	if len(stream.acks) != 1 || stream.acks[0] != "/next/2" {
		t.Errorf("acks = %v", stream.acks)
	}
	if len(stream.pulls) < 3 || stream.pulls[0] != "" || stream.pulls[1] != "" || stream.pulls[2] != "" {
		t.Errorf("pulls = %q", stream.pulls)
	}
}

type failingLedger struct {
	*ledger.Ledger
	failures int
}

func (l *failingLedger) Settle(ctx context.Context, id string) error {
	if l.failures > 0 {
		l.failures--
		return errors.New("database is down")
	}
	return l.Ledger.Settle(ctx, id)
}

func TestConsumeFailureNotAcked(t *testing.T) {
	f := newFixture(t)
	p := f.pay(t, f.resolve(t), 2590)
	flaky := &failingLedger{Ledger: f.ledger, failures: 1}
	svc := New(flaky, f.keys, f.dict, f.bank, f.store, walletISPB)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	msg := message(t, settled(p.EndToEndID))
	stream := &fakeStream{batches: [][]pix.Message{{msg}, {msg}}}
	stream.onDrained = cancel
	if err := svc.Consume(ctx, stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.acks) != 1 || stream.acks[0] != "/next/2" {
		t.Errorf("acks = %v, pulls = %q", stream.acks, stream.pulls)
	}
	if got, _ := f.svc.Get(context.Background(), p.ID); got.Status != Settled {
		t.Errorf("status = %s", got.Status)
	}
	if b := f.balance(t); b.AvailableCents != 100000-2590 || b.ReservedCents != 0 {
		t.Errorf("balance = %+v", b)
	}
}

func TestList(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	var ids []string // oldest first
	for i := range 5 {
		f.advance(time.Second)
		p := f.pay(t, f.resolve(t), int64(100+i))
		ids = append(ids, p.ID)
	}
	byID := func(id string) Payment { p, _ := f.svc.Get(ctx, id); return p }
	_ = f.svc.OnStatus(ctx, settled(byID(ids[1]).EndToEndID))
	_ = f.svc.OnStatus(ctx, rejected(byID(ids[2]).EndToEndID, "AC07"))

	page, err := f.svc.List(ctx, ListInput{AccountID: anaID, PageSize: 2})
	if err != nil || len(page.Payments) != 2 || page.NextToken == "" {
		t.Fatalf("%+v, %v", page, err)
	}
	if page.Payments[0].ID != ids[4] || page.Payments[1].ID != ids[3] {
		t.Errorf("not newest first: %v", []string{page.Payments[0].ID, page.Payments[1].ID})
	}
	page2, err := f.svc.List(ctx, ListInput{AccountID: anaID, PageSize: 2, PageToken: page.NextToken})
	if err != nil || len(page2.Payments) != 2 || page2.Payments[0].ID != ids[2] || page2.Payments[1].ID != ids[1] || page2.NextToken == "" {
		t.Fatalf("%+v, %v", page2, err)
	}
	page3, err := f.svc.List(ctx, ListInput{AccountID: anaID, PageSize: 2, PageToken: page2.NextToken})
	if err != nil || len(page3.Payments) != 1 || page3.Payments[0].ID != ids[0] || page3.NextToken != "" {
		t.Fatalf("%+v, %v", page3, err)
	}

	pending, err := f.svc.List(ctx, ListInput{AccountID: anaID, Statuses: []Status{Pending}})
	if err != nil || len(pending.Payments) != 3 {
		t.Fatalf("pending: %+v, %v", pending, err)
	}
	both, _ := f.svc.List(ctx, ListInput{AccountID: anaID, Statuses: []Status{Settled, Rejected}})
	if len(both.Payments) != 2 {
		t.Errorf("settled and rejected: %d", len(both.Payments))
	}

	if _, err := f.svc.List(ctx, ListInput{AccountID: anaID, PageToken: "garbage"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad token: %v", err)
	}
	if _, err := f.svc.List(ctx, ListInput{AccountID: anaID, PageSize: 101}); !errors.Is(err, ErrInvalid) {
		t.Errorf("page size: %v", err)
	}
	if _, err := f.svc.List(ctx, ListInput{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("no account: %v", err)
	}
	if _, err := f.svc.List(ctx, ListInput{AccountID: "0a1b2c3d-9999-4000-8000-000000000009"}); !errors.Is(err, ledger.ErrAccountNotFound) {
		t.Errorf("unknown account: %v", err)
	}
}

func TestGetNotFound(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"0a1b2c3d-9999-4000-8000-000000000009", "not-a-uuid"} {
		if _, err := f.svc.Get(context.Background(), id); !errors.Is(err, ErrPaymentNotFound) {
			t.Errorf("%q: %v", id, err)
		}
	}
	if _, err := f.svc.Get(context.Background(), ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty: %v", err)
	}
}

func (f *fixture) settledBookTransferID(t *testing.T) string {
	t.Helper()
	r, err := f.svc.ResolveKey(context.Background(), anaID, brunoKey)
	if err != nil {
		t.Fatal(err)
	}
	return f.pay(t, r, 100).EndToEndID
}

func TestInsertExisting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	r := f.resolve(t)

	in := Payment{AccountID: brunoID, Direction: Inbound, Type: TypeBookTransfer, Status: Settled, AmountCents: 100, EndToEndID: r.EndToEndID,
		Counterparty: r.Payee, CreatedAt: f.clock, FinishedAt: f.clock}
	first, created, err := f.store.Insert(ctx, in)
	if err != nil || !created || first.ID == "" || first.FinishedAt.IsZero() || first.ResolutionID != "" {
		t.Fatalf("first: %+v, %v, %v", first, created, err)
	}
	again, created, err := f.store.Insert(ctx, in)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("again: %+v, %v, %v", again, created, err)
	}

	out := in
	out.AccountID, out.Direction, out.Status, out.ResolutionID, out.FinishedAt = anaID, Outbound, Pending, r.ID, time.Time{}
	sent, created, err := f.store.Insert(ctx, out)
	if err != nil || !created || sent.ID == first.ID {
		t.Fatalf("sent: %+v, %v, %v", sent, created, err)
	}
	if got, found, _ := f.store.ByEndToEnd(ctx, r.EndToEndID, Inbound); !found || got.ID != first.ID {
		t.Errorf("received: %+v", got)
	}
	if got, found, _ := f.store.ByEndToEnd(ctx, r.EndToEndID, Outbound); !found || got.ID != sent.ID {
		t.Errorf("sent: %+v", got)
	}
}
