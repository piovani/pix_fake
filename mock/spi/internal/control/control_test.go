package control_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"pixfake/mock/spi/internal/control"
	"pixfake/mock/spi/internal/fault"
	"pixfake/mock/spi/internal/httpapi"
	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/seed"
	"pixfake/mock/spi/internal/spi"
)

const (
	wallet = "99000001"
	alfa   = "99000002"
	beta   = "99000003"
)

type env struct {
	t     *testing.T
	url   string
	svc   *spi.Service
	store *memstore.Store
}

// newEnv starts the whole stack the binary runs: API, processor, storage, seed accounts, faults and control.
func newEnv(t *testing.T) *env {
	t.Helper()
	store := memstore.New()
	if _, err := pix.SeedAccounts(context.Background(), store, seed.Accounts()); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	participants := pix.DefaultParticipants()
	svc := spi.NewService(store, 20*time.Millisecond)
	svc.SetHandler(pix.NewProcessor(svc, store, participants, pix.AccountResponder{Accounts: store}, log))
	faults := &fault.Set{}
	api := httpapi.New(svc, log, nil).WithParticipants(participants.Known).WithFaults(faults).
		WithControl(control.New(svc, store, participants, faults).Handler())
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, url: srv.URL, svc: svc, store: store}
}

// call sends a JSON request and returns the status and the JSON answer as a map.
func (e *env) call(method, path string, body any) (int, map[string]any, http.Header) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out); err != nil {
			e.t.Fatalf("%s %s: %v\n%s", method, path, err, raw)
		}
	}
	return resp.StatusCode, out, resp.Header
}

func str(m map[string]any, keys ...string) string {
	var cur any = m
	for _, k := range keys {
		mm, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = mm[k]
	}
	s, _ := cur.(string)
	return s
}

// read returns, and acknowledges, what is queued for the participant.
func (e *env) read(ispb string) []spi.Message {
	e.t.Helper()
	ctx := context.Background()
	token, _ := e.svc.StartStream(ctx, ispb)
	var all []spi.Message
	for {
		msgs, next, err := e.svc.Pull(ctx, ispb, token, spi.MaxMessages)
		if err != nil {
			e.t.Fatal(err)
		}
		token = next
		if len(msgs) == 0 {
			break
		}
		all = append(all, msgs...)
	}
	_ = e.svc.CloseStream(ctx, ispb, token)
	return all
}

// --- Inspection ---

func TestAccounts(t *testing.T) {
	e := newEnv(t)
	status, out, _ := e.call("GET", "/mock/accounts", nil)
	accts, _ := out["accounts"].([]any)
	if status != 200 || len(accts) != len(seed.Accounts()) {
		t.Fatalf("status %d, %d accounts; want %d", status, len(accts), len(seed.Accounts()))
	}
	found := map[string]string{}
	for _, a := range accts {
		m := a.(map[string]any)
		found[str(m, "number")] = str(m, "status") + "/" + str(m, "scenario")
	}
	if found[seed.ClosedAccount] != "CLOSED/NORMAL" || found[seed.SilentAccount] != "ACTIVE/NO_ANSWER" || found["00900004"] != "ACTIVE/REJECT:DS04" {
		t.Fatalf("accounts = %v", found)
	}
}

// --- A simulated bank pays the participant under test ---

func TestInboundPayment(t *testing.T) {
	e := newEnv(t)
	status, out, _ := e.call("POST", "/mock/inbound/payments", map[string]any{
		"from": alfa, "to": wallet, "to_account": "00123456", "amount": "10.50", "key": "ana.souza@example.com",
	})
	if status != http.StatusCreated {
		t.Fatalf("status = %d: %v", status, out)
	}
	id := str(out, "id")
	if !pix.ValidEndToEndID(id) || str(out, "status") != "PENDING" || str(out, "amount") != "10.50" || str(out, "kind") != "PAYMENT" ||
		str(out, "payer", "participant") != alfa || str(out, "payee", "participant") != wallet || str(out, "payee", "tax_id") != "52998224725" ||
		str(out, "payee", "key") != "ana.souza@example.com" || str(out, "initiation") != "DICT" {
		t.Fatalf("payment = %v", out)
	}
	// The payer is a real account of the bank: Carlos Mendes.
	if str(out, "payer", "account") != "00456789" || str(out, "payer", "tax_id") != "39053344705" {
		t.Fatalf("payer = %v", out["payer"])
	}

	// The wallet finds the order in its queue, as if Banco Alfa had sent it through the SPI.
	in := e.read(wallet)
	if len(in) != 1 {
		t.Fatalf("wallet received %d messages, want the pacs.008", len(in))
	}
	msg, err := iso.ParsePacs008(in[0].Body)
	if err != nil || msg.Transactions()[0].PmtID.EndToEnd != id || msg.Transactions()[0].CreditorAcct.Key() != "ana.souza@example.com" {
		t.Fatalf("forwarded order: %v", err)
	}

	// It can be looked at, and it settles when the wallet answers.
	status, got, _ := e.call("GET", "/mock/payments/"+id, nil)
	if status != 200 || str(got, "status") != "PENDING" {
		t.Fatalf("GET payment = %d %v", status, got)
	}
	answer, _ := iso.NewPacs002(wallet, pix.SPIISPB, "M"+wallet+"abcdefghijklmnopqrstuvw", iso.FormatTime(time.Now()),
		iso.NewTxStatus(id, iso.StatusAcceptedByReceiver)).Marshal()
	if _, err := e.svc.Receive(context.Background(), wallet, [][]byte{answer}); err != nil {
		t.Fatal(err)
	}
	if _, got, _ = e.call("GET", "/mock/payments/"+id, nil); str(got, "status") != "SETTLED" || str(got, "settled_at") == "" {
		t.Fatalf("after the wallet's answer: %v", got)
	}
}

func TestInboundPaymentToBank(t *testing.T) {
	e := newEnv(t)
	mine := "E99000002" + time.Now().UTC().Format("200601021504") + "ABCDEFGHIJK"
	status, out, _ := e.call("POST", "/mock/inbound/payments", map[string]any{
		"from": alfa, "payer_account": "00456789", "to": beta, "to_account": "00567890", "amount": "1.00", "end_to_end_id": mine,
	})
	if status != http.StatusCreated || str(out, "status") != "SETTLED" || str(out, "id") != mine {
		t.Fatalf("status %d: %v", status, out)
	}
}

func TestInboundPaymentUnseeded(t *testing.T) {
	e := newEnv(t)
	body := map[string]any{"from": alfa, "to": wallet, "to_account": "00777777", "amount": "5.00"}
	if status, out, _ := e.call("POST", "/mock/inbound/payments", body); status != 422 || !strings.Contains(str(out, "error"), "to_tax_id") {
		t.Fatalf("status %d: %v; want 422 asking for to_tax_id", status, out)
	}
	body["to_tax_id"] = "52998224725"
	status, out, _ := e.call("POST", "/mock/inbound/payments", body)
	if status != http.StatusCreated || str(out, "payee", "account") != "00777777" || str(out, "payee", "branch") != "0001" || str(out, "payee", "account_type") != "CACC" {
		t.Fatalf("status %d: %v", status, out)
	}
}

func TestInboundPaymentValidation(t *testing.T) {
	e := newEnv(t)
	ok := func() map[string]any {
		return map[string]any{"from": alfa, "to": wallet, "to_account": "00123456", "amount": "10.00"}
	}
	tests := map[string]func(map[string]any){
		"the wallet is not simulated": func(m map[string]any) {
			m["from"], m["to"], m["to_account"] = wallet, alfa, "00456789" // an order the SPI would accept, from a real participant
		},
		"unknown payer participant":     func(m map[string]any) { m["from"] = "12345678" },
		"unknown receiving participant": func(m map[string]any) { m["to"] = "12345678" },
		"same participant":              func(m map[string]any) { m["to"] = alfa },
		"zero amount":                   func(m map[string]any) { m["amount"] = "0.00" },
		"bad amount":                    func(m map[string]any) { m["amount"] = "ten" },
		"missing amount":                func(m map[string]any) { delete(m, "amount") },
		"bad end to end":                func(m map[string]any) { m["end_to_end_id"] = "E1" },
		"payer account of another bank": func(m map[string]any) { m["payer_account"] = "00567890" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			m := ok()
			mutate(m)
			if status, out, _ := e.call("POST", "/mock/inbound/payments", m); status != 422 {
				t.Fatalf("status = %d: %v; want 422", status, out)
			}
		})
	}
	if status, out, _ := e.call("POST", "/mock/inbound/payments", map[string]any{"from": alfa, "bogus": 1}); status != 400 {
		t.Fatalf("unknown field = %d: %v; want 400", status, out)
	}
	if n := len(e.read(wallet)); n != 0 {
		t.Fatalf("the wallet received %d messages from requests that were refused", n)
	}
}

func TestInboundPaymentRefused(t *testing.T) {
	e := newEnv(t)
	// An EndToEndId from 20 hours ago is refused by the SPI (FF08) before anything is stored.
	old := "E99000002" + time.Now().UTC().Add(-20*time.Hour).Format("200601021504") + "ABCDEFGHIJK"
	status, out, _ := e.call("POST", "/mock/inbound/payments", map[string]any{
		"from": alfa, "to": wallet, "to_account": "00123456", "amount": "1.00", "end_to_end_id": old,
	})
	if status != 422 || !strings.Contains(str(out, "messages"), "/mock/messages") {
		t.Fatalf("status %d: %v", status, out)
	}
	status, list, _ := e.call("GET", str(out, "messages"), nil)
	msgs, _ := list["messages"].([]any)
	if status != 200 || len(msgs) != 1 || !strings.Contains(str(msgs[0].(map[string]any), "body"), "FF08") {
		t.Fatalf("answer queued for the bank: %d %v", status, list)
	}
}

// --- A simulated bank returns a payment it received ---

func TestInboundReturn(t *testing.T) {
	e := newEnv(t)
	// The wallet pays Carlos Mendes at Banco Alfa (simulated): settled at once.
	order := walletPays(t, e, "E99000001"+time.Now().UTC().Format("200601021504")+"WALLETPAY01", "12.00")
	e.read(wallet)

	status, out, _ := e.call("POST", "/mock/inbound/returns", map[string]any{"end_to_end_id": order, "amount": "5.00"})
	if status != http.StatusCreated {
		t.Fatalf("status %d: %v", status, out)
	}
	rtr := str(out, "id")
	if !pix.ValidReturnID(rtr) || str(out, "kind") != "RETURN" || str(out, "status") != "PENDING" || str(out, "end_to_end_id") != order ||
		str(out, "amount") != "5.00" || str(out, "payer", "participant") != alfa || str(out, "payee", "participant") != wallet {
		t.Fatalf("return = %v", out)
	}
	in := e.read(wallet)
	if len(in) != 1 {
		t.Fatalf("wallet received %d messages, want the pacs.004", len(in))
	}
	if m, err := iso.ParsePacs004(in[0].Body); err != nil || m.Returns()[0].RtrID != rtr {
		t.Fatalf("forwarded return: %v", err)
	}

	// Without an amount the whole payment is returned; a second full return is refused by the receiving bank... here the
	// wallet is the receiver of the return, so it just stays pending until it answers.
	_, again, _ := e.call("POST", "/mock/inbound/returns", map[string]any{"end_to_end_id": order})
	if str(again, "amount") != "12.00" {
		t.Fatalf("default amount = %v", again["amount"])
	}
}

func TestInboundReturnValidation(t *testing.T) {
	e := newEnv(t)
	post := func(m map[string]any) (int, map[string]any) {
		s, o, _ := e.call("POST", "/mock/inbound/returns", m)
		return s, o
	}
	if s, _ := post(map[string]any{"end_to_end_id": "E99000001202611141200DOESNOTEXIST"}); s != 404 {
		t.Fatalf("unknown payment = %d, want 404", s)
	}

	// A payment the wallet received cannot be returned from the mock: the wallet is a real participant.
	_, in, _ := e.call("POST", "/mock/inbound/payments", map[string]any{"from": alfa, "to": wallet, "to_account": "00123456", "amount": "3.00"})
	e.read(wallet)
	if s, out := post(map[string]any{"end_to_end_id": str(in, "id")}); s != 422 || !strings.Contains(str(out, "error"), "PENDING") {
		t.Fatalf("pending payment = %d %v; want 422", s, out)
	}
	accept, _ := iso.NewPacs002(wallet, pix.SPIISPB, "M"+wallet+"abcdefghijklmnopqrstuvw", iso.FormatTime(time.Now()),
		iso.NewTxStatus(str(in, "id"), iso.StatusAcceptedByReceiver)).Marshal()
	_, _ = e.svc.Receive(context.Background(), wallet, [][]byte{accept})
	if s, out := post(map[string]any{"end_to_end_id": str(in, "id")}); s != 422 || !strings.Contains(str(out, "error"), "real participant") {
		t.Fatalf("payment received by the wallet = %d %v; want 422", s, out)
	}

	// Bad amount and bad id.
	order := walletPays(t, e, "E99000001"+time.Now().UTC().Format("200601021504")+"WALLETPAY02", "1.00")
	if s, _ := post(map[string]any{"end_to_end_id": order, "amount": "0.00"}); s != 422 {
		t.Fatalf("zero amount = %d", s)
	}
	if s, _ := post(map[string]any{"end_to_end_id": order, "return_id": "D1"}); s != 422 {
		t.Fatalf("bad return id = %d", s)
	}
}

// walletPays sends a pacs.008 from the wallet to Carlos Mendes (Banco Alfa) and returns its EndToEndId.
func walletPays(t *testing.T, e *env, e2e, amount string) string {
	t.Helper()
	tx := iso.NewCreditTransfer(e2e, amount, iso.FormatTime(time.Now()))
	tx.SetDebtor("Ana Souza", "52998224725", wallet, "0001", "00123456", "CACC")
	tx.SetCreditor("39053344705", alfa, "0001", "00456789", "CACC", "39053344705")
	body, _ := iso.NewPacs008(wallet, pix.SPIISPB, "M"+wallet+"abcdefghijklmnopqrstuvw", iso.FormatTime(time.Now()), "HIGH", "PAGPRI", tx).Marshal()
	if _, err := e.svc.Receive(context.Background(), wallet, [][]byte{body}); err != nil {
		t.Fatal(err)
	}
	if p, err := e.store.GetPayment(context.Background(), e2e); err != nil || p.Status != pix.StatusSettled {
		t.Fatalf("setup payment = %+v, %v", p, err)
	}
	return e2e
}

// --- Looking at the state, and clearing it ---

func TestInspect(t *testing.T) {
	e := newEnv(t)
	order := walletPays(t, e, "E99000001"+time.Now().UTC().Format("200601021504")+"WALLETPAY03", "2.00")

	status, list, _ := e.call("GET", "/mock/payments?status=SETTLED&participant="+wallet, nil)
	ps, _ := list["payments"].([]any)
	if status != 200 || len(ps) != 1 || str(ps[0].(map[string]any), "id") != order {
		t.Fatalf("payments = %d %v", status, list)
	}
	if _, none, _ := e.call("GET", "/mock/payments?status=PENDING", nil); len(none["payments"].([]any)) != 0 {
		t.Fatalf("pending payments = %v", none)
	}
	if s, _, _ := e.call("GET", "/mock/payments/E99000001202611141200NOTTHERE00", nil); s != 404 {
		t.Fatalf("unknown payment = %d", s)
	}

	status, msgs, _ := e.call("GET", "/mock/messages?participant="+wallet, nil)
	ms, _ := msgs["messages"].([]any)
	if status != 200 || len(ms) < 2 { // what the wallet sent and what it was answered
		t.Fatalf("messages = %d %v", status, msgs)
	}
	if s, _, _ := e.call("GET", "/mock/messages?direction=sideways", nil); s != 400 {
		t.Fatalf("bad direction = %d", s)
	}

	if s, _, _ := e.call("GET", "/mock/reports/E99000001202611141200NOTTHERE00", nil); s != 404 {
		t.Fatalf("unknown report = %d", s)
	}
	_, _ = e.store.AddReported(context.Background(), pix.ReportedTransaction{ID: "E99000001202611141200REPORTED001", EndToEndID: "E99000001202611141200REPORTED001",
		Scenario: "BOK1", Initiation: "MANU", AmountCents: 350, SettledAt: time.Now(), PayerISPB: wallet, PayeeISPB: wallet,
		PayerAccount: "1", PayeeAccount: "2", PayerAccountType: "CACC", PayeeAccountType: "SVGS", ReportedBy: wallet, ReportedAt: time.Now()})
	if s, out, _ := e.call("GET", "/mock/reports/E99000001202611141200REPORTED001", nil); s != 200 || str(out, "amount") != "3.50" {
		t.Fatalf("report = %d %v", s, out)
	}
}

func TestReset(t *testing.T) {
	e := newEnv(t)
	walletPays(t, e, "E99000001"+time.Now().UTC().Format("200601021504")+"WALLETPAY04", "2.00")
	_, _, _ = e.call("POST", "/mock/faults", map[string]any{"status": 503})

	if s, _, _ := e.call("POST", "/mock/reset", nil); s != http.StatusNoContent {
		t.Fatalf("reset = %d", s)
	}
	if _, out, _ := e.call("GET", "/mock/payments", nil); len(out["payments"].([]any)) != 0 {
		t.Fatalf("payments left: %v", out)
	}
	if _, out, _ := e.call("GET", "/mock/messages", nil); len(out["messages"].([]any)) != 0 {
		t.Fatalf("messages left: %v", out)
	}
	if _, out, _ := e.call("GET", "/mock/accounts", nil); len(out["accounts"].([]any)) != len(seed.Accounts()) {
		t.Fatalf("a reset must keep the accounts")
	}
	if _, out, _ := e.call("GET", "/mock/faults", nil); len(out["faults"].([]any)) != 0 {
		t.Fatalf("faults left: %v", out)
	}
	if s, _, _ := e.call("GET", "/api/v1/in/catalog", nil); s != 200 {
		t.Fatalf("the API still fails after a reset: %d", s)
	}
}

// --- Faults ---

func TestFaults(t *testing.T) {
	e := newEnv(t)
	status, out, _ := e.call("POST", "/mock/faults", map[string]any{"path_prefix": "/api/v1/in/", "status": 429, "retry_after": 3, "count": 2})
	if status != 201 || out["id"] == nil {
		t.Fatalf("add fault = %d %v", status, out)
	}

	for i := 0; i < 2; i++ {
		req, _ := http.NewRequest("POST", e.url+"/api/v1/in/"+wallet+"/msgs", strings.NewReader("<m/>"))
		req.Header.Set("Content-Type", "application/xml; charset=utf-8")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "3" || resp.Header.Get("Content-Type") != "application/problem+xml" ||
			!strings.Contains(string(body), "too-many-requests") {
			t.Fatalf("request %d = %d, Retry-After %q, %s", i, resp.StatusCode, resp.Header.Get("Retry-After"), body)
		}
	}
	// The rule was for two requests, and other paths were never affected.
	if s, _, _ := e.call("GET", "/api/v1/in/catalog", nil); s != 200 {
		t.Fatalf("catalog = %d", s)
	}
	req, _ := http.NewRequest("POST", e.url+"/api/v1/in/"+wallet+"/msgs", strings.NewReader("<m/>"))
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 201 {
		t.Fatalf("after the rule is spent = %d", resp.StatusCode)
	}
}

func TestFaultsAdmin(t *testing.T) {
	e := newEnv(t)
	_, a, _ := e.call("POST", "/mock/faults", map[string]any{"status": 503})
	_, _, _ = e.call("POST", "/mock/faults", map[string]any{"delay_ms": 10})
	if _, list, _ := e.call("GET", "/mock/faults", nil); len(list["faults"].([]any)) != 2 {
		t.Fatalf("faults = %v", list)
	}
	if s, _, _ := e.call("GET", "/api/v1/in/catalog", nil); s != 503 {
		t.Fatalf("catalog = %d, want 503", s)
	}
	if s, _, _ := e.call("DELETE", "/mock/faults/"+str2(a["id"]), nil); s != 204 {
		t.Fatalf("remove = %d", s)
	}
	if s, _, _ := e.call("DELETE", "/mock/faults/999", nil); s != 404 {
		t.Fatalf("remove unknown = %d", s)
	}
	if s, _, _ := e.call("GET", "/api/v1/in/catalog", nil); s != 200 {
		t.Fatalf("catalog after removing the 503 = %d", s)
	}
	if s, _, _ := e.call("DELETE", "/mock/faults", nil); s != 204 {
		t.Fatalf("clear = %d", s)
	}
	if _, list, _ := e.call("GET", "/mock/faults", nil); len(list["faults"].([]any)) != 0 {
		t.Fatalf("faults left = %v", list)
	}
}

func TestFaultsInvalid(t *testing.T) {
	e := newEnv(t)
	for name, rule := range map[string]map[string]any{
		"nothing":        {},
		"status 500":     {"status": 500},
		"targets /mock":  {"status": 503, "path_prefix": "/mock/faults"},
		"lower case":     {"status": 503, "method": "get"},
		"negative count": {"status": 503, "count": -1},
	} {
		if s, out, _ := e.call("POST", "/mock/faults", rule); s != 422 {
			t.Errorf("%s: status %d %v; want 422", name, s, out)
		}
	}
	if s, _, _ := e.call("POST", "/mock/faults", map[string]any{"status": 503, "bogus": true}); s != 400 {
		t.Errorf("unknown field = %d, want 400", s)
	}
}

func TestControlExempt(t *testing.T) {
	e := newEnv(t)
	_, _, _ = e.call("POST", "/mock/faults", map[string]any{"status": 503})
	if s, _, _ := e.call("GET", "/mock/accounts", nil); s != 200 {
		t.Fatalf("control API under a fault = %d", s)
	}
	resp, err := http.Get(e.url + "/healthz")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("health under a fault: %v", err)
	}
}

func TestUnknownEndpoint(t *testing.T) {
	e := newEnv(t)
	if s, _, _ := e.call("GET", "/mock/nope", nil); s != 404 {
		t.Fatalf("unknown control endpoint = %d", s)
	}
}

func str2(v any) string {
	f, _ := v.(float64)
	return strconv.Itoa(int(f))
}
