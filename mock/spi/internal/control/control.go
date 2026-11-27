// Package control is the control API of the SPI mock, under /mock. It is not part of the Bacen contract: it is
// how a test looks at the state of the mock, makes a simulated bank send something to the participant under
// test, clears the state and makes the API misbehave. It speaks JSON, to be easy to call from a script.
//
//	GET    /mock/accounts                       the test accounts, with their status and scenario
//	GET    /mock/payments[?status=&kind=&participant=&limit=]   payments and returns, newest first
//	GET    /mock/payments/{id}                  one of them, by EndToEndId or RtrId
//	GET    /mock/reports/{id}                   a transaction reported with a trck.002
//	GET    /mock/messages[?participant=&direction=in|out&limit=] the messages, with their XML
//	POST   /mock/inbound/payments               a simulated bank pays an account of another participant
//	POST   /mock/inbound/returns                a simulated bank returns a payment it received
//	POST   /mock/reset                          clears messages, payments and reports (accounts stay)
//	GET    /mock/faults, POST /mock/faults, DELETE /mock/faults[/{id}]   make the API answer 429/503 or late
package control

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"pixfake/mock/spi/internal/fault"
	"pixfake/mock/spi/internal/iso"
	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/spi"
)

// Backend is what the control API reads and clears.
type Backend interface {
	pix.Storage
	pix.AccountStore
	pix.Inspector
}

// Control serves the control API.
type Control struct {
	Svc          *spi.Service
	Store        Backend
	Participants pix.Participants
	Faults       *fault.Set
	Now          func() time.Time
}

// New creates the control API. faults is the set the API consults; when nil the control API gets one of its own.
func New(svc *spi.Service, store Backend, participants pix.Participants, faults *fault.Set) *Control {
	if faults == nil {
		faults = &fault.Set{}
	}
	return &Control{Svc: svc, Store: store, Participants: participants, Faults: faults, Now: time.Now}
}

// Handler returns the routes, all under /mock/.
func (c *Control) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mock/accounts", c.accounts)
	mux.HandleFunc("GET /mock/payments", c.payments)
	mux.HandleFunc("GET /mock/payments/{id}", c.payment)
	mux.HandleFunc("GET /mock/reports/{id}", c.report)
	mux.HandleFunc("GET /mock/messages", c.messages)
	mux.HandleFunc("POST /mock/inbound/payments", c.inboundPayment)
	mux.HandleFunc("POST /mock/inbound/returns", c.inboundReturn)
	mux.HandleFunc("POST /mock/reset", c.reset)
	mux.HandleFunc("GET /mock/faults", c.listFaults)
	mux.HandleFunc("POST /mock/faults", c.addFault)
	mux.HandleFunc("DELETE /mock/faults", c.clearFaults)
	mux.HandleFunc("DELETE /mock/faults/{id}", c.removeFault)
	mux.HandleFunc("/mock/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "no such control endpoint: "+r.URL.Path)
	})
	return mux
}

// --- Responses ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string, extra ...any) {
	body := map[string]any{"error": msg}
	for i := 0; i+1 < len(extra); i += 2 {
		body[fmt.Sprint(extra[i])] = extra[i+1]
	}
	writeJSON(w, status, body)
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func internal(w http.ResponseWriter, err error) {
	fail(w, http.StatusInternalServerError, err.Error())
}

func limitOf(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// money writes cents as "10.50".
func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

// --- Views ---

type partyView struct {
	Participant string `json:"participant"`
	TaxID       string `json:"tax_id"`
	Name        string `json:"name,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Account     string `json:"account"`
	Type        string `json:"account_type"`
	Key         string `json:"key,omitempty"`
}

type paymentView struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	EndToEndID   string     `json:"end_to_end_id"`
	Status       string     `json:"status"`
	ReasonCode   string     `json:"reason_code,omitempty"`
	ReasonDetail string     `json:"reason_detail,omitempty"`
	ReturnReason string     `json:"return_reason,omitempty"`
	Amount       string     `json:"amount"`
	Initiation   string     `json:"initiation,omitempty"`
	Info         string     `json:"info,omitempty"`
	Payer        partyView  `json:"payer"`
	Payee        partyView  `json:"payee"`
	CreatedAt    time.Time  `json:"created_at"`
	SettledAt    *time.Time `json:"settled_at,omitempty"`
}

func viewOf(p pix.Payment) paymentView {
	v := paymentView{
		ID: p.ID, Kind: p.Kind, EndToEndID: p.EndToEndID, Status: p.Status, ReasonCode: p.ReasonCode, ReasonDetail: p.ReasonDetail,
		ReturnReason: p.ReturnReason, Amount: money(p.AmountCents), Initiation: p.Initiation, Info: p.Info, CreatedAt: p.CreatedAt,
		Payer: partyView{p.PayerISPB, p.PayerTaxID, p.PayerName, p.PayerBranch, p.PayerAccount, p.PayerAccountType, ""},
		Payee: partyView{p.PayeeISPB, p.PayeeTaxID, "", p.PayeeBranch, p.PayeeAccount, p.PayeeAccountType, p.PayeeKey},
	}
	if !p.SettledAt.IsZero() {
		v.SettledAt = &p.SettledAt
	}
	return v
}

type accountView struct {
	Participant string `json:"participant"`
	Branch      string `json:"branch,omitempty"`
	Number      string `json:"number"`
	Type        string `json:"type"`
	TaxID       string `json:"holder_tax_id"`
	Name        string `json:"holder_name"`
	Status      string `json:"status"`
	// Scenario is how the simulated bank answers a payment: NORMAL, NO_ANSWER or REJECT:<code>.
	Scenario string `json:"scenario"`
}

// --- Inspection ---

func (c *Control) accounts(w http.ResponseWriter, r *http.Request) {
	list, err := c.Store.ListAccounts(r.Context())
	if err != nil {
		internal(w, err)
		return
	}
	out := make([]accountView, len(list))
	for i, a := range list {
		out[i] = accountView{a.ISPB, a.Branch, a.Number, a.Type, a.HolderTaxID, a.HolderName, a.Status, a.Scenario}
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

func (c *Control) payments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	list, err := c.Store.ListPayments(r.Context(), pix.PaymentFilter{
		Status: q.Get("status"), Kind: q.Get("kind"), Participant: q.Get("participant"), Limit: limitOf(r),
	})
	if err != nil {
		internal(w, err)
		return
	}
	out := make([]paymentView, len(list))
	for i, p := range list {
		out[i] = viewOf(p)
	}
	writeJSON(w, http.StatusOK, map[string]any{"payments": out})
}

func (c *Control) payment(w http.ResponseWriter, r *http.Request) {
	p, err := c.Store.GetPayment(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, pix.ErrPaymentNotFound):
		fail(w, http.StatusNotFound, "no payment or return with that id")
	case err != nil:
		internal(w, err)
	default:
		writeJSON(w, http.StatusOK, viewOf(p))
	}
}

func (c *Control) report(w http.ResponseWriter, r *http.Request) {
	t, err := c.Store.GetReported(r.Context(), r.PathValue("id"))
	switch {
	case errors.Is(err, pix.ErrReportedNotFound):
		fail(w, http.StatusNotFound, "no reported transaction with that id")
	case err != nil:
		internal(w, err)
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"id": t.ID, "end_to_end_id": t.EndToEndID, "scenario": t.Scenario, "initiation": t.Initiation,
			"amount": money(t.AmountCents), "settled_at": t.SettledAt, "reported_by": t.ReportedBy, "reported_at": t.ReportedAt,
			"payer": partyView{t.PayerISPB, t.PayerTaxID, "", t.PayerBranch, t.PayerAccount, t.PayerAccountType, ""},
			"payee": partyView{t.PayeeISPB, t.PayeeTaxID, "", t.PayeeBranch, t.PayeeAccount, t.PayeeAccountType, t.PayeeKey},
		})
	}
}

func (c *Control) messages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if d := q.Get("direction"); d != "" && d != "in" && d != "out" {
		fail(w, http.StatusBadRequest, `direction must be "in" or "out"`)
		return
	}
	list, err := c.Store.ListMessages(r.Context(), pix.MessageFilter{Participant: q.Get("participant"), Direction: q.Get("direction"), Limit: limitOf(r)})
	if err != nil {
		internal(w, err)
		return
	}
	if list == nil {
		list = []pix.StoredMessage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"messages": list})
}

func (c *Control) reset(w http.ResponseWriter, r *http.Request) {
	if err := c.Store.Reset(r.Context()); err != nil {
		internal(w, err)
		return
	}
	c.Faults.Clear()
	w.WriteHeader(http.StatusNoContent)
}

// --- Faults ---

func (c *Control) listFaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"faults": c.Faults.List()})
}

func (c *Control) addFault(w http.ResponseWriter, r *http.Request) {
	var rule fault.Rule
	if !decode(w, r, &rule) {
		return
	}
	id, err := c.Faults.Add(rule)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (c *Control) clearFaults(w http.ResponseWriter, r *http.Request) {
	c.Faults.Clear()
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) removeFault(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || !c.Faults.Remove(id) {
		fail(w, http.StatusNotFound, "no such fault")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- Events from a simulated bank ---

const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomText(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

// newID builds an identifier: letter + ISPB + yyyyMMddHHmm (UTC) + 11 characters.
func (c *Control) newID(letter, ispb string) string {
	return letter + ispb + c.Now().UTC().Format("200601021504") + randomText(11)
}

type inboundPayment struct {
	From         string `json:"from"`                    // simulated participant that pays
	PayerAccount string `json:"payer_account,omitempty"` // an account of From; default: its first active account that answers normally
	To           string `json:"to"`                      // participant that receives the money (the wallet, usually)
	ToAccount    string `json:"to_account"`
	ToTaxID      string `json:"to_tax_id,omitempty"` // needed only when ToAccount is not in the seed
	ToBranch     string `json:"to_branch,omitempty"`
	ToType       string `json:"to_type,omitempty"`
	Key          string `json:"key,omitempty"` // the Pix key that was used, if any
	Amount       string `json:"amount"`
	EndToEndID   string `json:"end_to_end_id,omitempty"`
}

func (c *Control) inboundPayment(w http.ResponseWriter, r *http.Request) {
	var req inboundPayment
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()

	if !c.Participants.Simulated(req.From) {
		fail(w, http.StatusUnprocessableEntity, "from must be a simulated participant (one the mock answers for): "+req.From+" is not")
		return
	}
	if !c.Participants.Known(req.To) || req.To == req.From {
		fail(w, http.StatusUnprocessableEntity, "to must be a registered participant other than from")
		return
	}
	if cents, ok := pix.ParseAmount(req.Amount); !ok || cents == 0 {
		fail(w, http.StatusUnprocessableEntity, `amount must be a positive value such as "10.50"`)
		return
	}
	if req.EndToEndID != "" && !pix.ValidEndToEndID(req.EndToEndID) {
		fail(w, http.StatusUnprocessableEntity, "end_to_end_id is not a valid EndToEndId")
		return
	}

	payer, err := c.payerAccount(r, req.From, req.PayerAccount)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	taxID, branch, accType := req.ToTaxID, req.ToBranch, req.ToType
	if acct, err := c.Store.GetAccount(ctx, req.To, req.ToAccount); err == nil {
		taxID, branch, accType = orDefault(taxID, acct.HolderTaxID), orDefault(branch, acct.Branch), orDefault(accType, acct.Type)
	} else if !errors.Is(err, pix.ErrAccountNotFound) {
		internal(w, err)
		return
	} else if taxID == "" {
		fail(w, http.StatusUnprocessableEntity, "to_account is not in the seed: say who holds it with to_tax_id (and to_branch, to_type)")
		return
	}
	branch, accType = orDefault(branch, "0001"), orDefault(accType, "CACC")

	e2e := orDefault(req.EndToEndID, c.newID("E", req.From))
	now := c.Now().UTC()
	tx := iso.NewCreditTransfer(e2e, req.Amount, iso.FormatTime(now))
	tx.SetDebtor(payer.HolderName, payer.HolderTaxID, req.From, payer.Branch, payer.Number, payer.Type)
	tx.SetCreditor(taxID, req.To, branch, req.ToAccount, accType, req.Key)
	msgID := "M" + req.From + randomText(23)
	body, err := iso.NewPacs008(req.From, pix.SPIISPB, msgID, iso.FormatTime(now), "HIGH", "PAGPRI", tx).Marshal()
	if err != nil {
		internal(w, err)
		return
	}

	// The order goes through the same path as one sent by a real participant: validation, forwarding, settlement.
	if _, err := c.Svc.Receive(ctx, req.From, [][]byte{body}); err != nil {
		internal(w, err)
		return
	}
	c.answer(w, r, e2e, req.From)
}

func (c *Control) payerAccount(r *http.Request, ispb, number string) (pix.Account, error) {
	if number != "" {
		a, err := c.Store.GetAccount(r.Context(), ispb, number)
		if errors.Is(err, pix.ErrAccountNotFound) {
			return pix.Account{}, fmt.Errorf("payer_account %s is not an account of %s", number, ispb)
		}
		return a, err
	}
	all, err := c.Store.ListAccounts(r.Context())
	if err != nil {
		return pix.Account{}, err
	}
	for _, a := range all {
		if a.ISPB == ispb && a.Status == pix.AccountActive && a.Scenario == pix.ScenarioNormal {
			return a, nil
		}
	}
	return pix.Account{}, fmt.Errorf("%s has no active account that answers normally to pay from", ispb)
}

type inboundReturn struct {
	EndToEndID string `json:"end_to_end_id"` // the payment to return
	Amount     string `json:"amount,omitempty"`
	ReturnID   string `json:"return_id,omitempty"`
	Reason     string `json:"reason,omitempty"` // MD06 (default), or BE08 and FR01 of the MED
}

func (c *Control) inboundReturn(w http.ResponseWriter, r *http.Request) {
	var req inboundReturn
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()

	orig, err := c.Store.GetPayment(ctx, req.EndToEndID)
	switch {
	case errors.Is(err, pix.ErrPaymentNotFound) || (err == nil && orig.Kind != pix.KindPayment):
		fail(w, http.StatusNotFound, "no payment with that end_to_end_id")
		return
	case err != nil:
		internal(w, err)
		return
	case orig.Status != pix.StatusSettled:
		fail(w, http.StatusUnprocessableEntity, "only a settled payment can be returned; this one is "+orig.Status)
		return
	case !c.Participants.Simulated(orig.PayeeISPB):
		fail(w, http.StatusUnprocessableEntity, "the payment was received by "+orig.PayeeISPB+", which is a real participant: it sends its own returns")
		return
	}
	amount := orDefault(req.Amount, money(orig.AmountCents))
	if cents, ok := pix.ParseAmount(amount); !ok || cents == 0 {
		fail(w, http.StatusUnprocessableEntity, `amount must be a positive value such as "10.50"`)
		return
	}
	reason := orDefault(req.Reason, "MD06")
	if reason != "MD06" && reason != "BE08" && reason != "FR01" {
		fail(w, http.StatusUnprocessableEntity, "reason must be MD06, BE08 or FR01")
		return
	}
	if req.ReturnID != "" && !pix.ValidReturnID(req.ReturnID) {
		fail(w, http.StatusUnprocessableEntity, "return_id is not a valid RtrId")
		return
	}

	sender := orig.PayeeISPB
	rtr := orDefault(req.ReturnID, c.newID("D", sender))
	now := iso.FormatTime(c.Now())
	ret := iso.NewReturn(rtr, orig.EndToEndID, amount, reason, sender, orig.PayerISPB)
	body, err := iso.NewPacs004(sender, pix.SPIISPB, "M"+sender+randomText(23), now, ret).Marshal()
	if err != nil {
		internal(w, err)
		return
	}
	if _, err := c.Svc.Receive(ctx, sender, [][]byte{body}); err != nil {
		internal(w, err)
		return
	}
	c.answer(w, r, rtr, sender)
}

// answer reports what became of the order a simulated bank just sent.
func (c *Control) answer(w http.ResponseWriter, r *http.Request, id, sender string) {
	p, err := c.Store.GetPayment(r.Context(), id)
	switch {
	case errors.Is(err, pix.ErrPaymentNotFound):
		// The SPI refused it before storing it; its pacs.002 (or admi.002) is in the sender's queue.
		fail(w, http.StatusUnprocessableEntity, "the SPI rejected the order; see the answer it queued for "+sender,
			"id", id, "messages", "/mock/messages?direction=out&participant="+sender+"&limit=1")
	case err != nil:
		internal(w, err)
	default:
		writeJSON(w, http.StatusCreated, viewOf(p))
	}
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
