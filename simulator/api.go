package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	walletv1 "pixfake/wallet/gen/wallet/v1"
)

type sim struct {
	cfg      config
	http     *http.Client
	accounts walletv1.AccountServiceClient
	keys     walletv1.KeyServiceClient
	payments walletv1.PaymentServiceClient
	health   grpc_health_v1.HealthClient
}

func newSim(cfg config, conn *grpc.ClientConn) *sim {
	return &sim{
		cfg:      cfg,
		http:     &http.Client{Timeout: 15 * time.Second, Transport: otelhttp.NewTransport(http.DefaultTransport)},
		accounts: walletv1.NewAccountServiceClient(conn),
		keys:     walletv1.NewKeyServiceClient(conn),
		payments: walletv1.NewPaymentServiceClient(conn),
		health:   grpc_health_v1.NewHealthClient(conn),
	}
}

func (s *sim) routes(m *http.ServeMux) {
	m.HandleFunc("GET /api/config", s.config)
	m.HandleFunc("GET /api/status", s.status)

	m.HandleFunc("GET /api/wallet/accounts", s.walletAccounts)
	m.HandleFunc("GET /api/wallet/accounts/{id}/payments", s.walletPayments)
	m.HandleFunc("GET /api/wallet/accounts/{id}/statement", s.walletStatement)
	m.HandleFunc("GET /api/wallet/accounts/{id}/keys", s.walletKeys)
	m.HandleFunc("POST /api/wallet/accounts/{id}/keys", s.walletRegisterKey)
	m.HandleFunc("POST /api/wallet/accounts/{id}/keys/remove", s.walletRemoveKey)
	m.HandleFunc("POST /api/wallet/accounts/{id}/status", s.walletSetStatus)
	m.HandleFunc("POST /api/wallet/accounts/{id}/limit", s.walletSetLimit)
	m.HandleFunc("POST /api/wallet/pay", s.walletPay)
	m.HandleFunc("POST /api/wallet/return", s.walletReturn)
	m.HandleFunc("GET /api/wallet/reports", s.walletReports)
	m.HandleFunc("POST /api/wallet/reports/{id}/resend", s.walletResendReport)

	m.HandleFunc("GET /api/spi/accounts", s.proxy(s.cfg.SPI, "/mock/accounts"))
	m.HandleFunc("GET /api/spi/payments", s.proxy(s.cfg.SPI, "/mock/payments"))
	m.HandleFunc("GET /api/spi/messages", s.proxy(s.cfg.SPI, "/mock/messages"))
	m.HandleFunc("POST /api/spi/pay", s.forward(s.cfg.SPI, "/mock/inbound/payments"))
	m.HandleFunc("POST /api/spi/return", s.forward(s.cfg.SPI, "/mock/inbound/returns"))

	m.HandleFunc("GET /api/dict/keys", s.proxy(s.cfg.DICT, "/mock/entries"))
	m.HandleFunc("POST /api/dict/keys", s.dictCreate)
	m.HandleFunc("POST /api/dict/keys/remove", s.dictRemove)

	m.HandleFunc("GET /api/track/{e2e}", s.track)

	m.HandleFunc("GET /api/faults", s.faults)
	m.HandleFunc("POST /api/faults/{target}", s.addFault)
	m.HandleFunc("POST /api/faults/clear", s.clearFaults)
	m.HandleFunc("POST /api/reset", s.reset)
}

// The wallet's test accounts, as in its seed.
var walletAccounts = []struct{ ID, Name, Branch, Number string }{
	{"0a1b2c3d-0001-4000-8000-000000000001", "Ana Souza", "0001", "00123456"},
	{"0a1b2c3d-0002-4000-8000-000000000002", "Bruno Lima", "0001", "00234567"},
	{"0a1b2c3d-0003-4000-8000-000000000003", "Padaria Pão Quente Ltda", "0001", "00345678"},
}

// --- helpers ---

func withTraceID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if sc := trace.SpanContextFromContext(r.Context()); sc.HasTraceID() {
			w.Header().Set("X-Trace-Id", sc.TraceID().String())
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	msg := err.Error()
	if st, ok := status.FromError(err); ok {
		msg = st.Code().String() + ": " + st.Message()
		code = http.StatusBadGateway
		if st.Code() < 17 && st.Code() > 0 {
			code = http.StatusUnprocessableEntity
		}
	}
	writeJSON(w, code, map[string]string{"error": msg})
}

var pjson = protojson.MarshalOptions{UseProtoNames: true}

// pj writes a wallet message as the JSON of its proto, so enums are names and not numbers.
func pj(m proto.Message) json.RawMessage {
	b, err := pjson.Marshal(m)
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

func pjList[T proto.Message](list []T) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(list))
	for _, m := range list {
		out = append(out, pj(m))
	}
	return out
}

func decode(r *http.Request, v any) error {
	return json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(v)
}

func tag(ctx context.Context, kv ...attribute.KeyValue) {
	trace.SpanFromContext(ctx).SetAttributes(kv...)
}

func uuid() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func ctxFor(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 15*time.Second)
}

func (s *sim) call(ctx context.Context, method, url string, body []byte, header map[string]string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return resp.StatusCode, out, err
}

// proxy passes a read of a mock's control API through, query string included.
func (s *sim) proxy(base, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := ctxFor(r)
		defer cancel()
		code, body, err := s.call(ctx, http.MethodGet, base+path+"?"+r.URL.RawQuery, nil, nil)
		if err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}
}

// forward passes a JSON action to a mock's control API.
func (s *sim) forward(base, path string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := ctxFor(r)
		defer cancel()
		in, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		code, body, err := s.call(ctx, http.MethodPost, base+path, in, map[string]string{"Content-Type": "application/json"})
		if err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
		var out struct {
			ID         string `json:"id"`
			EndToEndID string `json:"end_to_end_id"`
		}
		if json.Unmarshal(body, &out) == nil && out.ID != "" {
			tag(ctx, attribute.String("pix.end_to_end_id", out.ID))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_, _ = w.Write(body)
	}
}

// --- status and config ---

func (s *sim) config(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"grafana": s.cfg.Grafana, "accounts": walletAccounts})
}

func (s *sim) status(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	up := func(url string) bool {
		code, _, err := s.call(ctx, http.MethodGet, url, nil, nil)
		return err == nil && code == http.StatusOK
	}
	res, err := s.health.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
	writeJSON(w, http.StatusOK, map[string]bool{
		"dict":    up(s.cfg.DICT + "/healthz"),
		"spi":     up(s.cfg.SPI + "/healthz"),
		"wallet":  err == nil && res.Status == grpc_health_v1.HealthCheckResponse_SERVING,
		"grafana": up(s.cfg.Grafana + "/api/health"),
	})
}

// --- wallet ---

func (s *sim) walletAccounts(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	type view struct {
		ID, Name, Branch, Number string
		Available, Reserved      int64
		Error                    string `json:",omitempty"`
	}
	var out []view
	for _, a := range walletAccounts {
		v := view{ID: a.ID, Name: a.Name, Branch: a.Branch, Number: a.Number}
		if b, err := s.accounts.GetBalance(ctx, &walletv1.GetBalanceRequest{AccountId: a.ID}); err != nil {
			v.Error = err.Error()
		} else {
			v.Available, v.Reserved = b.AvailableCents, b.ReservedCents
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *sim) walletPayments(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	res, err := s.payments.ListPayments(ctx, &walletv1.ListPaymentsRequest{AccountId: r.PathValue("id"), PageSize: 30})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pjList(res.Payments))
}

func (s *sim) walletStatement(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	res, err := s.accounts.GetStatement(ctx, &walletv1.GetStatementRequest{AccountId: r.PathValue("id"), PageSize: 30})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pjList(res.Entries))
}

func (s *sim) walletKeys(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	res, err := s.keys.ListKeys(ctx, &walletv1.ListKeysRequest{AccountId: r.PathValue("id")})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pjList(res.Keys))
}

func (s *sim) walletRegisterKey(w http.ResponseWriter, r *http.Request) {
	var in struct{ Type, Key string }
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := ctxFor(r)
	defer cancel()
	t := walletv1.KeyType(walletv1.KeyType_value["KEY_TYPE_"+strings.ToUpper(in.Type)])
	res, err := s.keys.RegisterKey(ctx, &walletv1.RegisterKeyRequest{AccountId: r.PathValue("id"), Type: t, Key: in.Key, RequestId: uuid()})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pj(res.Key))
}

func (s *sim) walletRemoveKey(w http.ResponseWriter, r *http.Request) {
	var in struct{ Key string }
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := ctxFor(r)
	defer cancel()
	if _, err := s.keys.RemoveKey(ctx, &walletv1.RemoveKeyRequest{AccountId: r.PathValue("id"), Key: in.Key}); err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"removed": in.Key})
}

func (s *sim) walletPay(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AccountID   string `json:"account_id"`
		Key         string `json:"key"`
		AmountCents int64  `json:"amount_cents"`
		Message     string `json:"message"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := ctxFor(r)
	defer cancel()
	res, err := s.payments.ResolveKey(ctx, &walletv1.ResolveKeyRequest{AccountId: in.AccountID, Key: in.Key})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	p, err := s.payments.CreatePayment(ctx, &walletv1.CreatePaymentRequest{ResolutionId: res.ResolutionId, AmountCents: in.AmountCents, Message: in.Message})
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": status.Code(err).String() + ": " + status.Convert(err).Message(), "payee": pj(res.Payee)})
		return
	}
	tag(ctx, attribute.String("pix.end_to_end_id", p.Payment.EndToEndId))
	writeJSON(w, http.StatusOK, map[string]any{"payee": pj(res.Payee), "book_transfer": res.BookTransfer, "payment": pj(p.Payment)})
}

func (s *sim) walletReturn(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PaymentID   string `json:"payment_id"`
		AmountCents int64  `json:"amount_cents"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := ctxFor(r)
	defer cancel()
	res, err := s.payments.ReturnPayment(ctx, &walletv1.ReturnPaymentRequest{PaymentId: in.PaymentID, AmountCents: in.AmountCents, RequestId: uuid()})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pj(res.Payment))
}

// The wallet has no gRPC call for operations, so the simulator runs its operations commands, as an operator would.
func (s *sim) walletCmd(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"run"}, args...)...)
	cmd.Dir = s.cfg.WalletDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *sim) walletSetStatus(w http.ResponseWriter, r *http.Request) {
	var in struct{ Status string }
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.walletCmd(ctx, "./cmd/accounts", "status", r.PathValue("id"), strings.ToUpper(in.Status))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": out})
}

func (s *sim) walletSetLimit(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Cents int64 `json:"cents"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.walletCmd(ctx, "./cmd/accounts", "limit", r.PathValue("id"), fmt.Sprint(in.Cents))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": out})
}

func (s *sim) walletReports(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.walletCmd(ctx, "./cmd/reports", "list")
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	type row struct{ ID, EndToEndID, Reason, Cents string }
	rows := []row{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, "\t"); len(f) == 4 {
			rows = append(rows, row{f[0], f[1], f[2], f[3]})
		}
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *sim) walletResendReport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	out, err := s.walletCmd(ctx, "./cmd/reports", "resend", r.PathValue("id"))
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": out})
}

// --- DICT, as another participant ---

type dictAccount struct {
	Participant   string `xml:"Participant"`
	Branch        string `xml:"Branch"`
	AccountNumber string `xml:"AccountNumber"`
	AccountType   string `xml:"AccountType"`
	OpeningDate   string `xml:"OpeningDate"`
}

type dictOwner struct {
	Type        string `xml:"Type"`
	TaxIDNumber string `xml:"TaxIdNumber"`
	Name        string `xml:"Name"`
}

type dictEntry struct {
	Key     string      `xml:"Key,omitempty"`
	KeyType string      `xml:"KeyType"`
	Account dictAccount `xml:"Account"`
	Owner   dictOwner   `xml:"Owner"`
}

func (s *sim) dictCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Participant, KeyType, Key, Branch, Account, AccountType, TaxID, Name string
	}
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	owner := "NATURAL_PERSON"
	if len(in.TaxID) == 14 {
		owner = "LEGAL_PERSON"
	}
	body, _ := xml.Marshal(struct {
		XMLName   xml.Name  `xml:"CreateEntryRequest"`
		Entry     dictEntry `xml:"Entry"`
		Reason    string    `xml:"Reason"`
		RequestID string    `xml:"RequestId"`
	}{
		Entry: dictEntry{Key: in.Key, KeyType: strings.ToUpper(in.KeyType),
			Account: dictAccount{in.Participant, in.Branch, in.Account, in.AccountType, time.Now().UTC().Format("2006-01-02T15:04:05.000Z")},
			Owner:   dictOwner{owner, in.TaxID, in.Name}},
		Reason: "USER_REQUESTED", RequestID: uuid(),
	})
	ctx, cancel := ctxFor(r)
	defer cancel()
	code, out, err := s.call(ctx, http.MethodPost, s.cfg.DICT+"/api/v2/entries", body,
		map[string]string{"Content-Type": "application/xml", "X-Participant-ISPB": in.Participant})
	s.dictAnswer(w, code, out, err)
}

func (s *sim) dictRemove(w http.ResponseWriter, r *http.Request) {
	var in struct{ Participant, Key string }
	if err := decode(r, &in); err != nil {
		fail(w, http.StatusBadRequest, err)
		return
	}
	body, _ := xml.Marshal(struct {
		XMLName     xml.Name `xml:"DeleteEntryRequest"`
		Key         string   `xml:"Key"`
		Participant string   `xml:"Participant"`
		Reason      string   `xml:"Reason"`
	}{Key: in.Key, Participant: in.Participant, Reason: "USER_REQUESTED"})
	ctx, cancel := ctxFor(r)
	defer cancel()
	code, out, err := s.call(ctx, http.MethodPost, s.cfg.DICT+"/api/v2/entries/"+urlKey(in.Key)+"/delete", body,
		map[string]string{"Content-Type": "application/xml", "X-Participant-ISPB": in.Participant})
	s.dictAnswer(w, code, out, err)
}

func urlKey(k string) string {
	return strings.ReplaceAll(strings.ReplaceAll(k, "+", "%2B"), "@", "%40")
}

func (s *sim) dictAnswer(w http.ResponseWriter, code int, body []byte, err error) {
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	if code >= 300 {
		var p struct {
			Title  string `xml:"title"`
			Detail string `xml:"detail"`
		}
		_ = xml.Unmarshal(body, &p)
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": fmt.Sprintf("DICT %d %s: %s", code, p.Title, p.Detail), "xml": string(body)})
		return
	}
	var e struct {
		Entry dictEntry `xml:"Entry"`
	}
	_ = xml.Unmarshal(body, &e)
	writeJSON(w, http.StatusOK, map[string]any{"key": e.Entry.Key, "xml": string(body)})
}

// --- tracking one payment across the wallet and the SPI ---

func (s *sim) track(w http.ResponseWriter, r *http.Request) {
	e2e := r.PathValue("e2e")
	ctx, cancel := ctxFor(r)
	defer cancel()
	out := map[string]any{"end_to_end_id": e2e}

	if code, body, err := s.call(ctx, http.MethodGet, s.cfg.SPI+"/mock/payments/"+e2e, nil, nil); err == nil && code == http.StatusOK {
		out["spi"] = json.RawMessage(body)
	}
	var wallet []*walletv1.Payment
	for _, a := range walletAccounts {
		res, err := s.payments.ListPayments(ctx, &walletv1.ListPaymentsRequest{AccountId: a.ID, PageSize: 100})
		if err != nil {
			continue
		}
		for _, p := range res.Payments {
			if p.EndToEndId == e2e || strings.Contains(r.URL.Query().Get("ids"), p.Id) {
				wallet = append(wallet, p)
			}
		}
	}
	out["wallet"] = pjList(wallet)
	writeJSON(w, http.StatusOK, out)
}

// --- faults and reset ---

func (s *sim) faults(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	out := map[string]json.RawMessage{}
	for name, base := range map[string]string{"dict": s.cfg.DICT, "spi": s.cfg.SPI} {
		if code, body, err := s.call(ctx, http.MethodGet, base+"/mock/faults", nil, nil); err == nil && code == http.StatusOK {
			out[name] = body
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *sim) base(target string) string {
	if target == "dict" {
		return s.cfg.DICT
	}
	return s.cfg.SPI
}

func (s *sim) addFault(w http.ResponseWriter, r *http.Request) {
	in, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	ctx, cancel := ctxFor(r)
	defer cancel()
	code, body, err := s.call(ctx, http.MethodPost, s.base(r.PathValue("target"))+"/mock/faults", in, map[string]string{"Content-Type": "application/json"})
	if err != nil {
		fail(w, http.StatusBadGateway, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

func (s *sim) clearFaults(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	for _, base := range []string{s.cfg.DICT, s.cfg.SPI} {
		if _, _, err := s.call(ctx, http.MethodDelete, base+"/mock/faults", nil, nil); err != nil {
			fail(w, http.StatusBadGateway, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "faults cleared"})
}

func (s *sim) reset(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := ctxFor(r)
	defer cancel()
	for _, base := range []string{s.cfg.DICT, s.cfg.SPI} {
		if code, body, err := s.call(ctx, http.MethodPost, base+"/mock/reset", nil, nil); err != nil || code >= 300 {
			fail(w, http.StatusBadGateway, fmt.Errorf("reset %s: %v %s", base, err, body))
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"result": "both mocks are back to their seed"})
}
