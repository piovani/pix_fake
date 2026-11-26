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

	"pixfake/mock/dict/internal/control"
	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/fault"
	"pixfake/mock/dict/internal/httpapi"
	"pixfake/mock/dict/internal/memstore"
	"pixfake/mock/dict/internal/seed"
)

type env struct {
	t     *testing.T
	url   string
	store *memstore.Store
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memstore.New()
	svc := dict.NewService(store)
	reseed := func(ctx context.Context) error { _, err := svc.Seed(ctx, seed.Entries()); return err }
	if err := reseed(context.Background()); err != nil {
		t.Fatal(err)
	}
	faults := &fault.Set{}
	api := httpapi.New(svc, slog.New(slog.NewTextHandler(io.Discard, nil)), nil).WithFaults(faults).
		WithControl(control.New(store, reseed, faults).WithTransactions(svc).Handler())
	srv := httptest.NewServer(api.Handler())
	t.Cleanup(srv.Close)
	return &env{t: t, url: srv.URL, store: store}
}

func (e *env) has(key string) bool {
	_, err := e.store.Get(context.Background(), key)
	return err == nil
}

func (e *env) call(method, path string, body any, headers map[string]string) (int, map[string]any, http.Header) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.url+path, rd)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(raw, &out)
	}
	out["_body"] = string(raw)
	return resp.StatusCode, out, resp.Header
}

// lookup is a payer's participant asking the DICT for a key.
var lookup = map[string]string{
	"PI-RequestingParticipant": "99000001", "PI-PayerId": "52998224725", "PI-EndToEndId": "E9900000120261114120000000000001",
	"X-Participant-ISPB": "99000001",
}

func TestEntriesAreListed(t *testing.T) {
	e := newEnv(t)
	status, out, _ := e.call("GET", "/mock/entries?participant=99000002", nil, nil)
	list, _ := out["entries"].([]any)
	if status != 200 || len(list) == 0 {
		t.Fatalf("status %d, %d entries", status, len(list))
	}
	for _, v := range list {
		m := v.(map[string]any)
		if m["account"].(map[string]any)["participant"] != "99000002" {
			t.Fatalf("entry of another participant: %v", m)
		}
	}
	if _, all, _ := e.call("GET", "/mock/entries?limit=3", nil, nil); len(all["entries"].([]any)) != 3 {
		t.Fatalf("limit not applied: %d", len(all["entries"].([]any)))
	}
}

func TestReset(t *testing.T) {
	e := newEnv(t)
	// A participant registers a key of its own.
	create := `<CreateEntryRequest><Entry><KeyType>PHONE</KeyType><Key>+5511988887777</Key><Account><Participant>99000001</Participant><Branch>0001</Branch>` +
		`<AccountNumber>00999999</AccountNumber><AccountType>CACC</AccountType><OpeningDate>2020-01-01T00:00:00.000Z</OpeningDate></Account>` +
		`<Owner><Type>NATURAL_PERSON</Type><TaxIdNumber>52998224725</TaxIdNumber><Name>Ana Souza</Name></Owner></Entry>` +
		`<Reason>USER_REQUESTED</Reason><RequestId>5c1f4ac6-8d2e-4f6b-9a4e-1f2d3c4b5a69</RequestId></CreateEntryRequest>`
	req, _ := http.NewRequest("POST", e.url+"/api/v2/entries", strings.NewReader(create))
	req.Header.Set("X-Participant-ISPB", "99000001")
	req.Header.Set("Content-Type", "application/xml")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create = %v, %v", resp, err)
	}
	if !e.has("+5511988887777") {
		t.Fatal("the key was not registered")
	}

	if s, _, _ := e.call("POST", "/mock/reset", nil, nil); s != http.StatusNoContent {
		t.Fatalf("reset = %d", s)
	}
	if e.has("+5511988887777") {
		t.Fatal("the key registered during the test survived the reset")
	}
	if !e.has("39053344705") {
		t.Fatal("the seed data was not loaded again")
	}
	if got := len(seed.Entries()); len(mustList(t, e)) != got {
		t.Fatalf("entries after the reset = %d, want %d", len(mustList(t, e)), got)
	}
}

func mustList(t *testing.T, e *env) []any {
	t.Helper()
	_, out, _ := e.call("GET", "/mock/entries?limit=500", nil, nil)
	return out["entries"].([]any)
}

func TestFaults(t *testing.T) {
	e := newEnv(t)
	status, out, _ := e.call("POST", "/mock/faults", map[string]any{"path_prefix": "/api/v2/entries", "status": 429, "retry_after": 5, "count": 1}, nil)
	if status != 201 || out["id"] == nil {
		t.Fatalf("add fault = %d %v", status, out)
	}
	status, out, hdr := e.call("GET", "/api/v2/entries/39053344705", nil, lookup)
	if status != 429 || hdr.Get("Retry-After") != "5" || hdr.Get("Content-Type") != "application/problem+xml" ||
		!strings.Contains(out["_body"].(string), "TooManyRequests") || !strings.Contains(out["_body"].(string), "<correlationId>") {
		t.Fatalf("faulted lookup = %d, Retry-After %q: %v", status, hdr.Get("Retry-After"), out["_body"])
	}
	if status, _, _ = e.call("GET", "/api/v2/entries/39053344705", nil, lookup); status != 200 {
		t.Fatalf("after the rule is spent = %d", status)
	}

	_, a, _ := e.call("POST", "/mock/faults", map[string]any{"status": 503, "retry_after": 2}, nil)
	status, out, hdr = e.call("GET", "/api/v2/entries/39053344705", nil, lookup)
	if status != 503 || hdr.Get("Retry-After") != "2" || !strings.Contains(out["_body"].(string), "ServiceUnavailable") {
		t.Fatalf("503 lookup = %d, Retry-After %q", status, hdr.Get("Retry-After"))
	}
	// The control API and the health check keep working while the API is down.
	if s, _, _ := e.call("GET", "/mock/entries?limit=1", nil, nil); s != 200 {
		t.Fatalf("control API under a fault = %d", s)
	}
	if s, _, _ := e.call("DELETE", "/mock/faults/"+strconv.Itoa(int(a["id"].(float64))), nil, nil); s != 204 {
		t.Fatalf("remove fault = %d", s)
	}
	if s, _, _ := e.call("GET", "/api/v2/entries/39053344705", nil, lookup); s != 200 {
		t.Fatalf("after removing the fault = %d", s)
	}
}

func TestFaultsAdmin(t *testing.T) {
	e := newEnv(t)
	if s, _, _ := e.call("POST", "/mock/faults", map[string]any{"status": 500}, nil); s != 422 {
		t.Fatalf("status 500 = %d, want 422", s)
	}
	if s, _, _ := e.call("POST", "/mock/faults", map[string]any{"bogus": 1}, nil); s != 400 {
		t.Fatalf("unknown field = %d, want 400", s)
	}
	_, _, _ = e.call("POST", "/mock/faults", map[string]any{"delay_ms": 5}, nil)
	if _, list, _ := e.call("GET", "/mock/faults", nil, nil); len(list["faults"].([]any)) != 1 {
		t.Fatalf("faults = %v", list)
	}
	if s, _, _ := e.call("DELETE", "/mock/faults/999", nil, nil); s != 404 {
		t.Fatalf("remove unknown = %d", s)
	}
	if s, _, _ := e.call("DELETE", "/mock/faults", nil, nil); s != 204 {
		t.Fatalf("clear = %d", s)
	}
	if _, list, _ := e.call("GET", "/mock/faults", nil, nil); len(list["faults"].([]any)) != 0 {
		t.Fatalf("faults left = %v", list)
	}
	if s, _, _ := e.call("GET", "/mock/nope", nil, nil); s != 404 {
		t.Fatalf("unknown control endpoint = %d", s)
	}
}

// A transaction told to the DICT through the control API can be reported, and a reset forgets it.
func TestTransactions(t *testing.T) {
	e := newEnv(t)
	tx := map[string]any{
		"end_to_end_id": "E9900000120261120100000000000001", "amount": "150.00",
		"debtor":   map[string]any{"participant": "99000001", "tax_id": "52998224725"},
		"creditor": map[string]any{"participant": "99000002", "tax_id": "39053344705", "key": "+5521999990003"},
	}
	if status, out, _ := e.call("POST", "/mock/transactions", tx, nil); status != http.StatusNoContent {
		t.Fatalf("add = %d %v", status, out["_body"])
	}
	status, out, _ := e.call("GET", "/mock/transactions", nil, nil)
	list, _ := out["transactions"].([]any)
	if status != 200 || len(list) != 1 || list[0].(map[string]any)["amount"] != "150.00" || list[0].(map[string]any)["settled_at"] == nil {
		t.Fatalf("list = %d %v", status, out["_body"])
	}

	report := `<CreateInfractionReportRequest><Participant>99000001</Participant><InfractionReport><TransactionId>E9900000120261120100000000000001</TransactionId>` +
		`<Reason>REFUND_REQUEST</Reason><SituationType>SCAM</SituationType></InfractionReport></CreateInfractionReportRequest>`
	req, _ := http.NewRequest("POST", e.url+"/api/v2/infraction-reports", strings.NewReader(report))
	req.Header.Set("X-Participant-ISPB", "99000001")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("report on the transaction = %d", resp.StatusCode)
	}

	bad := map[string]any{"end_to_end_id": "E1", "amount": "1.234", "debtor": map[string]any{}, "creditor": map[string]any{}}
	if status, _, _ := e.call("POST", "/mock/transactions", bad, nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("bad amount = %d", status)
	}
	bad["amount"] = "1.00"
	if status, out, _ := e.call("POST", "/mock/transactions", bad, nil); status != http.StatusUnprocessableEntity || out["violations"] == nil {
		t.Fatalf("bad fields = %d %v", status, out["_body"])
	}
	if status, _, _ := e.call("POST", "/mock/transactions", map[string]any{"nope": 1}, nil); status != http.StatusBadRequest {
		t.Fatalf("unknown field = %d", status)
	}

	if status, _, _ := e.call("POST", "/mock/reset", nil, nil); status != http.StatusNoContent {
		t.Fatalf("reset = %d", status)
	}
	if _, out, _ := e.call("GET", "/mock/transactions", nil, nil); len(out["transactions"].([]any)) != 0 {
		t.Fatalf("after reset = %v", out["_body"])
	}
}
