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
	"pixfake/mock/spi/internal/seed"
	"pixfake/mock/spi/internal/spi"
)

const base = httpapi.BasePath

// newServer starts the real API (handlers + service + payment logic + Postgres) in an
// httptest.Server. Without SPI_TEST_DATABASE_URL the test is skipped (see pgtest).
func newServer(t *testing.T) (*httptest.Server, *spi.Service) {
	t.Helper()
	pool := pgtest.NewPool(t)
	return start(t, postgres.NewRepository(pool), pool.Ping)
}

// backend is what the SPI stores: messages, payments and accounts. The in-memory store and Postgres both do.
type backend interface {
	spi.Store
	pix.PaymentStore
	pix.AccountStore
}

// start runs the SPI as the binary does, with the seeded accounts and the account rules.
func start(t *testing.T, b backend, health func(context.Context) error) (*httptest.Server, *spi.Service) {
	t.Helper()
	if _, err := pix.SeedAccounts(context.Background(), b, seed.Accounts()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := spi.NewService(b, 50*time.Millisecond)
	svc.SetHandler(pix.NewProcessor(svc, b, pix.DefaultParticipants(), pix.AccountResponder{Accounts: b}, log))
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

// pacs008 builds an order from payer to the account of a seeded holder.
func pacs008(t *testing.T, payer string, to pix.Account, id string) []byte {
	t.Helper()
	now := time.Now().UTC()
	tx := iso.NewCreditTransfer(id, "25.90", iso.FormatTime(now))
	tx.SetDebtor("Ana Souza", "52998224725", payer, "0001", "00123456", "CACC")
	tx.SetCreditor(to.HolderTaxID, to.ISPB, "0001", to.Number, to.Type, to.HolderTaxID)
	body, err := iso.NewPacs008(payer, pix.SPIISPB, "M"+payer+"abcdefghijklmnopqrstuvw", iso.FormatTime(now), "HIGH", "PAGPRI", tx).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// account returns a seeded account.
func account(t *testing.T, number string) pix.Account {
	t.Helper()
	for _, a := range seed.Accounts() {
		if a.Number == number {
			return a
		}
	}
	t.Fatalf("account %s is not in the seed", number)
	return pix.Account{}
}

func pacs004(t *testing.T, sender, to, e2e, rtrID string) []byte {
	t.Helper()
	r := iso.NewReturn(rtrID, e2e, "25.90", "MD06", sender, to)
	body, err := iso.NewPacs004(sender, pix.SPIISPB, "M"+sender+"abcdefghijklmnopqrstuvw", iso.FormatTime(time.Now()), r).Marshal()
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
	post(t, url, wallet, pacs008(t, wallet, account(t, "00567890"), idOut))
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusSettledPayer || st.OrgnlEndToEnd != idOut {
		t.Fatalf("outbound payment: %+v; want ACSC for %s", st, idOut)
	}

	// Banco Alfa pays the wallet: the wallet receives the order, accepts it and both get the confirmation.
	idIn := e2eID(alfa, "INBOUND0001")
	post(t, url, alfa, pacs008(t, alfa, account(t, "00123456"), idIn))
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

	// Banco Beta returns the payment it received from the wallet: the wallet gets the pacs.004, accepts it,
	// and the return settles for both. (Beta's stream still holds the confirmation of the payment itself.)
	if st := status(t, readOne(t, url, beta)); st.Status != iso.StatusSettledReceiver || st.OrgnlEndToEnd != idOut {
		t.Fatalf("confirmation of the payment to Beta: %+v; want ACCC", st)
	}
	idRet := "D" + beta + time.Now().UTC().Format("200601021504") + "RETURN00001"
	post(t, url, beta, pacs004(t, beta, wallet, idOut, idRet))
	fwdReturn, err := iso.ParsePacs004([]byte(readOne(t, url, wallet)))
	if err != nil || fwdReturn.Returns()[0].RtrID != idRet || fwdReturn.Returns()[0].OrgnlEndToEnd != idOut {
		t.Fatalf("forwarded return: %v", err)
	}
	accept, err := iso.NewPacs002(wallet, pix.SPIISPB, "M"+wallet+"abcdefghijklmnopqrstuvw", iso.FormatTime(time.Now()),
		iso.NewTxStatus(idOut, iso.StatusAcceptedByReceiver).ForReturn(idRet)).Marshal()
	if err != nil {
		t.Fatal(err)
	}
	post(t, url, wallet, accept)
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusSettledReceiver || st.OrgnlInstrID != idRet || st.OrgnlEndToEnd != idOut {
		t.Fatalf("return confirmation to the wallet: %+v; want ACCC for %s", st, idRet)
	}
	if st := status(t, readOne(t, url, beta)); st.Status != iso.StatusSettledPayer || st.OrgnlInstrID != idRet {
		t.Fatalf("return confirmation to the sender: %+v; want ACSC", st)
	}

	// A payment between two customers of the wallet is a book transfer: the SPI refuses it.
	idBook := e2eID(wallet, "BOOKTRANSF1")
	post(t, url, wallet, pacs008(t, wallet, account(t, "00234567"), idBook))
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusRejected || st.ReasonCode() != "AG12" {
		t.Fatalf("book transfer: %+v; want RJCT AG12", st)
	}

	// The receiving bank rejects a payment to a closed account.
	idClosed := e2eID(wallet, "CLOSEDACCT1")
	post(t, url, wallet, pacs008(t, wallet, account(t, seed.ClosedAccount), idClosed))
	if st := status(t, readOne(t, url, wallet)); st.Status != iso.StatusRejected || st.ReasonCode() != "AC07" || st.OrgnlEndToEnd != idClosed {
		t.Fatalf("closed account: %+v; want RJCT AC07", st)
	}

	// A message the SPI cannot process is answered with an admi.002.
	post(t, url, wallet, []byte("<not-iso/>"))
	if a, err := iso.ParseAdmi002([]byte(readOne(t, url, wallet))); err != nil || a.AppHdr.MsgDefIdr != iso.Admi002ID || a.Ref() == "" {
		t.Fatalf("error notification: %+v, %v", a, err)
	}
}

func TestPaymentCycleOverHTTP(t *testing.T) {
	srv, _ := start(t, memstore.New(), nil)
	runPaymentCycle(t, srv.URL)
}

func TestPaymentCycleOverHTTPOnPostgres(t *testing.T) {
	srv, _ := newServer(t)
	runPaymentCycle(t, srv.URL)
}
