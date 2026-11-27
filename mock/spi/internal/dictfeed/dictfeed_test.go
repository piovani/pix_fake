package dictfeed_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"pixfake/mock/spi/internal/dictfeed"
	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

// fakeDICT is the control API of the DICT: it answers with the next status of answers, 204 when there are none.
type fakeDICT struct {
	mu       sync.Mutex
	answers  []int
	received []map[string]any
}

func (d *fakeDICT) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/mock/transactions" || r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "unexpected request", http.StatusNotFound)
		return
	}
	status := http.StatusNoContent
	if len(d.answers) > 0 {
		status, d.answers = d.answers[0], d.answers[1:]
	}
	if status == http.StatusNoContent {
		var v map[string]any
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &v)
		d.received = append(d.received, v)
	}
	w.WriteHeader(status)
}

func (d *fakeDICT) ids() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, len(d.received))
	for i, v := range d.received {
		out[i], _ = v["end_to_end_id"].(string)
	}
	return out
}

func settle(t *testing.T, s *memstore.Store, id string, cents int64, settledAt time.Time) {
	t.Helper()
	p := pix.Payment{
		ID: id, Kind: pix.KindPayment, EndToEndID: id, Status: pix.StatusPending, AmountCents: cents,
		PayerISPB: "99000001", PayerTaxID: "52998224725", PayeeISPB: "99000002", PayeeTaxID: "39053344705", PayeeKey: "maria@example.com",
		CreatedAt: settledAt, AcceptedAt: settledAt,
	}
	if _, _, err := s.CreatePayment(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.FinalizePayment(context.Background(), id, pix.StatusSettled, "", "", settledAt); err != nil {
		t.Fatal(err)
	}
}

func newFeed(t *testing.T, d *fakeDICT) (*dictfeed.Feed, *memstore.Store) {
	t.Helper()
	srv := httptest.NewServer(d)
	t.Cleanup(srv.Close)
	store := memstore.New()
	f := dictfeed.New(srv.URL+"/", store, slog.New(slog.DiscardHandler))
	f.SetClock(func() time.Time { return now })
	return f, store
}

const (
	e2e1 = "E9900000120261114120000000000001"
	e2e2 = "E9900000120261114120000000000002"
)

func TestFlushSendsTheSettledPayments(t *testing.T) {
	d := &fakeDICT{}
	f, store := newFeed(t, d)
	settle(t, store, e2e2, 15000, now.Add(-time.Minute))
	settle(t, store, e2e1, 1050, now.Add(-time.Hour))

	if n, err := f.Flush(context.Background()); err != nil || n != 2 {
		t.Fatalf("flush = %d, %v", n, err)
	}
	if got := d.ids(); len(got) != 2 || got[0] != e2e1 || got[1] != e2e2 {
		t.Fatalf("DICT received %v, want the oldest first", got)
	}
	got := d.received[0]
	want := map[string]any{
		"end_to_end_id": e2e1, "amount": "10.50", "settled_at": "2026-11-14T11:00:00Z",
		"debtor":   map[string]any{"participant": "99000001", "tax_id": "52998224725"},
		"creditor": map[string]any{"participant": "99000002", "tax_id": "39053344705", "key": "maria@example.com"},
	}
	if a, b := mustJSON(t, got), mustJSON(t, want); a != b {
		t.Fatalf("body = %s\nwant   %s", a, b)
	}

	if n, err := f.Flush(context.Background()); err != nil || n != 0 || len(d.ids()) != 2 {
		t.Fatalf("second flush = %d, %v; sent %v: nothing should be sent again", n, err, d.ids())
	}
}

func TestFlushKeepsWhatTheDICTDidNotTake(t *testing.T) {
	d := &fakeDICT{answers: []int{http.StatusServiceUnavailable}}
	f, store := newFeed(t, d)
	settle(t, store, e2e1, 1050, now.Add(-time.Hour))
	settle(t, store, e2e2, 1050, now.Add(-time.Minute))

	if n, err := f.Flush(context.Background()); err == nil || n != 0 {
		t.Fatalf("flush = %d, %v; want the 503 as an error", n, err)
	}
	if n, err := f.Flush(context.Background()); err != nil || n != 2 {
		t.Fatalf("flush after the DICT is back = %d, %v", n, err)
	}
	if got := d.ids(); len(got) != 2 || got[0] != e2e1 {
		t.Fatalf("DICT received %v", got)
	}
}

func TestFlushDropsWhatTheDICTRefuses(t *testing.T) {
	d := &fakeDICT{answers: []int{http.StatusUnprocessableEntity}}
	f, store := newFeed(t, d)
	settle(t, store, e2e1, 1050, now.Add(-time.Hour))
	settle(t, store, e2e2, 1050, now.Add(-time.Minute))

	if n, err := f.Flush(context.Background()); err != nil || n != 1 {
		t.Fatalf("flush = %d, %v; want the refused one dropped and the next sent", n, err)
	}
	if got := d.ids(); len(got) != 1 || got[0] != e2e2 {
		t.Fatalf("DICT received %v", got)
	}
	if n, _ := f.Flush(context.Background()); n != 0 {
		t.Fatalf("the refused payment was sent again")
	}
}

func TestFlushSkipsPaymentsTooOldToReturn(t *testing.T) {
	d := &fakeDICT{}
	f, store := newFeed(t, d)
	settle(t, store, e2e1, 1050, now.Add(-91*24*time.Hour))
	if n, err := f.Flush(context.Background()); err != nil || n != 0 {
		t.Fatalf("flush = %d, %v", n, err)
	}
}

func TestFlushFailsWhenTheDICTIsDown(t *testing.T) {
	store := memstore.New()
	settle(t, store, e2e1, 1050, now.Add(-time.Hour))
	f := dictfeed.New("http://127.0.0.1:1", store, slog.New(slog.DiscardHandler))
	f.SetClock(func() time.Time { return now })
	if _, err := f.Flush(context.Background()); err == nil {
		t.Fatal("flush to a DICT that is down did not fail")
	}
	if p, _ := store.PendingNotifications(context.Background(), time.Time{}, 10); len(p) != 1 {
		t.Fatalf("outbox has %d, want the payment kept", len(p))
	}
}

func TestRun(t *testing.T) {
	d := &fakeDICT{answers: []int{http.StatusServiceUnavailable}}
	f, store := newFeed(t, d)
	settle(t, store, e2e1, 1050, now.Add(-time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.Run(ctx, 5*time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for len(d.ids()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if got := d.ids(); len(got) != 1 {
		t.Fatalf("DICT received %v, want the payment after the 503", got)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
