// Package e2e tests the complete HTTP API on a real Postgres.
package e2e_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"pixfake/mock/spi/internal/httpapi"
	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/postgres"
	"pixfake/mock/spi/internal/postgres/pgtest"
	"pixfake/mock/spi/internal/spi"
)

const base = httpapi.BasePath

// newServer starts the real API (handlers + service + payment logic + Postgres) in an
// httptest.Server. Without SPI_TEST_DATABASE_URL the test is skipped (see pgtest).
func newServer(t *testing.T) (*httptest.Server, *spi.Service) {
	t.Helper()
	pool := pgtest.NewPool(t)
	repo := postgres.NewRepository(pool)
	return start(t, repo, repo, pool.Ping)
}

func start(t *testing.T, store spi.Store, payments pix.PaymentStore, health func(context.Context) error) (*httptest.Server, *spi.Service) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := spi.NewService(store, 50*time.Millisecond)
	svc.SetHandler(pix.NewProcessor(svc, payments, pix.DefaultParticipants(), pix.AcceptAll{}, log))
	srv := httptest.NewServer(httpapi.New(svc, log, health).Handler())
	t.Cleanup(srv.Close)
	return srv, svc
}

func call(t *testing.T, method, url string, body []byte, headers map[string]string) (*http.Response, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, rd)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestSendThenReadFlow(t *testing.T) {
	srv, svc := newServer(t)

	// A participant sends a message: it is stored and gets a ResourceId.
	resp, _ := call(t, http.MethodPost, srv.URL+base+"/in/11111111/msgs", []byte("<m>in</m>"),
		map[string]string{"Content-Type": "application/xml; charset=utf-8"})
	if resp.StatusCode != http.StatusCreated || len(resp.Header.Get("PI-ResourceId")) != 32 {
		t.Fatalf("send = %d, PI-ResourceId %q", resp.StatusCode, resp.Header.Get("PI-ResourceId"))
	}

	// The SPI queues a message for a participant, which reads it, closes the stream and does not get it again.
	id, err := svc.Deliver(context.Background(), "22222222", []byte("<m>out</m>"))
	if err != nil {
		t.Fatal(err)
	}
	resp, body := call(t, http.MethodGet, srv.URL+base+"/out/22222222/stream/start", nil, nil)
	if resp.StatusCode != 200 || body != "<m>out</m>" || resp.Header.Get("PI-ResourceId") != id {
		t.Fatalf("read = %d %q %q", resp.StatusCode, body, resp.Header.Get("PI-ResourceId"))
	}
	next := resp.Header.Get("PI-Pull-Next")
	if resp, _ = call(t, http.MethodDelete, srv.URL+next, nil, nil); resp.StatusCode != 200 {
		t.Fatalf("delete = %d", resp.StatusCode)
	}
	resp, _ = call(t, http.MethodGet, srv.URL+base+"/out/22222222/stream/start", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("read after ack = %d, want 204", resp.StatusCode)
	}
	if resp, _ = call(t, http.MethodGet, srv.URL+base+"/out/22222222/stream/0", nil, nil); resp.StatusCode != http.StatusGone {
		t.Fatalf("token from before the ack = %d, want 410", resp.StatusCode)
	}
}

func TestHealthzChecksDatabase(t *testing.T) {
	srv, _ := newServer(t)
	if resp, body := call(t, http.MethodGet, srv.URL+"/healthz", nil, nil); resp.StatusCode != 200 || !strings.Contains(body, "ok") {
		t.Fatalf("healthz = %d %q", resp.StatusCode, body)
	}
}

// --- The payment cycle over HTTP ---

const (
	wallet = "99000001"
	alfa   = "99000002"
	beta   = "99000003"
	xmlCT  = "application/xml; charset=utf-8"
)

func e2eID(payer string, suffix string) string {
	return "E" + payer + time.Now().UTC().Format("200601021504") + suffix
}

func pacs008(t *testing.T, payer, payee, id string) []byte {
	t.Helper()
	now := time.Now().UTC()
	tx := iso.NewCreditTransfer(id, "25.90", iso.FormatTime(now))
	tx.SetDebtor("Ana Souza", "52998224725", payer, "0001", "00123456", "CACC")
	tx.SetCreditor("39053344705", payee, "0001", "00456789", "CACC", "39053344705")
	body, err := iso.NewPacs008(payer, pix.SPIISPB, "M"+payer+"abcdefghijklmnopqrstuvw", iso.FormatTime(now), "HIGH", "PAGPRI", tx).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func pacs002(t *testing.T, from, id, status string) []byte {
	t.Helper()
	body, err := iso.NewPacs002(from, pix.SPIISPB, "M"+from+"abcdefghijklmnopqrstuvw", iso.FormatTime(time.Now()), iso.NewTxStatus(id, status)).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func post(t *testing.T, url, ispb string, body []byte) {
	t.Helper()
	resp, out := call(t, http.MethodPost, url+base+"/in/"+ispb+"/msgs", body, map[string]string{"Content-Type": xmlCT})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("send as %s = %d: %s", ispb, resp.StatusCode, out)
	}
}

// readOne reads one message from the participant's output and closes the stream.
func readOne(t *testing.T, url, ispb string) string {
	t.Helper()
	resp, body := call(t, http.MethodGet, url+base+"/out/"+ispb+"/stream/start", nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("read as %s = %d: %s", ispb, resp.StatusCode, body)
	}
	if r, _ := call(t, http.MethodDelete, url+resp.Header.Get("PI-Pull-Next"), nil, nil); r.StatusCode != http.StatusOK {
		t.Fatalf("close stream of %s = %d", ispb, r.StatusCode)
	}
	return body
}

func status(t *testing.T, body string) iso.TxStatus {
	t.Helper()
	m, err := iso.ParsePacs002([]byte(body))
	if err != nil || len(m.Statuses()) != 1 {
		t.Fatalf("not a pacs.002 with one status (%v):\n%s", err, body)
	}
	return m.Statuses()[0]
}

func runPaymentCycle(t *testing.T, url string) {
	t.Helper()

	// The wallet pays a customer of Banco Beta, which the mock simulates: settled at once.
	idOut := e2eID(wallet, "OUTBOUND001")
	post(t, url, wallet, pacs008(t, wallet, beta, idOut))
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusSettledPayer || st.OrgnlEndToEnd != idOut {
		t.Fatalf("outbound payment: %+v; want ACSC for %s", st, idOut)
	}

	// Banco Alfa pays the wallet: the wallet receives the order, accepts it and both get the confirmation.
	idIn := e2eID(alfa, "INBOUND0001")
	post(t, url, alfa, pacs008(t, alfa, wallet, idIn))
	fwd, err := iso.ParsePacs008([]byte(readOne(t, url, wallet)))
	if err != nil || fwd.Transactions()[0].PmtID.EndToEnd != idIn {
		t.Fatalf("forwarded order: %v", err)
	}
	post(t, url, wallet, pacs002(t, wallet, idIn, iso.StatusAcceptedByReceiver))
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusSettledReceiver || st.OrgnlEndToEnd != idIn {
		t.Fatalf("receiver confirmation: %+v; want ACCC", st)
	}
	if st := status(t, readOne(t, url, alfa)); st.Status != iso.StatusSettledPayer || st.OrgnlEndToEnd != idIn {
		t.Fatalf("payer confirmation: %+v; want ACSC", st)
	}

	// A payment between two customers of the wallet is a book transfer: the SPI refuses it.
	idBook := e2eID(wallet, "BOOKTRANSF1")
	post(t, url, wallet, pacs008(t, wallet, wallet, idBook))
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusRejected || st.ReasonCode() != "AG12" {
		t.Fatalf("book transfer: %+v; want RJCT AG12", st)
	}
}

func TestPaymentCycleOverHTTP(t *testing.T) {
	store := memstore.New()
	srv, _ := start(t, store, store, nil)
	runPaymentCycle(t, srv.URL)
}

func TestPaymentCycleOverHTTPOnPostgres(t *testing.T) {
	srv, _ := newServer(t)
	runPaymentCycle(t, srv.URL)
}
