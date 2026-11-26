// Package control is the control API of the DICT mock, under /mock. It is not part of the Bacen contract: it is how
// a test looks at the registered keys, puts the mock back to its seed data and makes the API misbehave. It speaks
// JSON, to be easy to call from a script.
//
//	GET    /mock/entries[?participant=&limit=]   the registered keys, ordered by key
//	POST   /mock/reset                           removes every entry and loads the seed data again
//	GET    /mock/faults, POST /mock/faults, DELETE /mock/faults[/{id}]   make the API answer 429/503 or late
//	GET    /mock/transactions, POST /mock/transactions   the transactions settled in the SPI that the DICT knows (MED)
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"pixfake/mock/dict/internal/dict"
	"pixfake/mock/dict/internal/fault"
)

// Store is what the control API needs from the storage of the entries.
type Store interface {
	DeleteAll(ctx context.Context) error
	ListEntries(ctx context.Context, participant string, limit int) ([]dict.Entry, error)
}

// Transactions is how the control API tells the DICT of transactions settled in the SPI.
type Transactions interface {
	AddTransaction(ctx context.Context, t dict.Transaction) error
	Transactions(ctx context.Context) ([]dict.Transaction, error)
}

// Control serves the control API.
type Control struct {
	Store Store
	// Reseed loads the seed data again; it runs after the entries are removed.
	Reseed       func(ctx context.Context) error
	Faults       *fault.Set
	Transactions Transactions // nil: no /mock/transactions
}

// New creates the control API. faults is the set the API consults; when nil the control API gets one of its own.
func New(store Store, reseed func(ctx context.Context) error, faults *fault.Set) *Control {
	if faults == nil {
		faults = &fault.Set{}
	}
	return &Control{Store: store, Reseed: reseed, Faults: faults}
}

// WithTransactions serves /mock/transactions with t (the DICT service).
func (c *Control) WithTransactions(t Transactions) *Control {
	c.Transactions = t
	return c
}

// Handler returns the routes, all under /mock/.
func (c *Control) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /mock/entries", c.entries)
	mux.HandleFunc("POST /mock/reset", c.reset)
	mux.HandleFunc("GET /mock/faults", c.listFaults)
	mux.HandleFunc("POST /mock/faults", c.addFault)
	mux.HandleFunc("DELETE /mock/faults", c.clearFaults)
	mux.HandleFunc("DELETE /mock/faults/{id}", c.removeFault)
	if c.Transactions != nil {
		mux.HandleFunc("GET /mock/transactions", c.transactions)
		mux.HandleFunc("POST /mock/transactions", c.addTransaction)
	}
	mux.HandleFunc("/mock/", func(w http.ResponseWriter, r *http.Request) {
		fail(w, http.StatusNotFound, "no such control endpoint: "+r.URL.Path)
	})
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

type entryView struct {
	Key     string `json:"key"`
	KeyType string `json:"key_type"`
	Account struct {
		Participant string    `json:"participant"`
		Branch      string    `json:"branch,omitempty"`
		Number      string    `json:"number"`
		Type        string    `json:"type"`
		OpenedAt    time.Time `json:"opened_at"`
	} `json:"account"`
	Owner struct {
		Type      string `json:"type"`
		TaxID     string `json:"tax_id"`
		Name      string `json:"name"`
		TradeName string `json:"trade_name,omitempty"`
	} `json:"owner"`
	CreatedAt time.Time `json:"created_at"`
	CID       string    `json:"cid"`
}

func (c *Control) entries(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	list, err := c.Store.ListEntries(r.Context(), r.URL.Query().Get("participant"), limit)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]entryView, len(list))
	for i, e := range list {
		v := entryView{Key: e.Key, KeyType: string(e.KeyType), CreatedAt: e.CreationDate, CID: e.CID}
		v.Account.Participant, v.Account.Branch, v.Account.Number = e.Account.Participant, e.Account.Branch, e.Account.AccountNumber
		v.Account.Type, v.Account.OpenedAt = string(e.Account.Type), e.Account.OpeningDate
		v.Owner.Type, v.Owner.TaxID, v.Owner.Name, v.Owner.TradeName = string(e.Owner.Type), e.Owner.TaxID, e.Owner.Name, e.Owner.TradeName
		out[i] = v
	}
	writeJSON(w, http.StatusOK, map[string]any{"entries": out})
}

func (c *Control) reset(w http.ResponseWriter, r *http.Request) {
	if err := c.Store.DeleteAll(r.Context()); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if c.Reseed != nil {
		if err := c.Reseed(r.Context()); err != nil {
			fail(w, http.StatusInternalServerError, fmt.Sprintf("the entries were removed but the seed data could not be loaded: %v", err))
			return
		}
	}
	c.Faults.Clear()
	w.WriteHeader(http.StatusNoContent)
}

func (c *Control) listFaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"faults": c.Faults.List()})
}

func (c *Control) addFault(w http.ResponseWriter, r *http.Request) {
	var rule fault.Rule
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rule); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
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

type partyView struct {
	Participant string `json:"participant"`
	TaxID       string `json:"tax_id"`
	Key         string `json:"key,omitempty"` // creditor only: the key that was paid
}

type transactionView struct {
	EndToEndID string    `json:"end_to_end_id"`
	Amount     string    `json:"amount"` // reais, "150.00"
	SettledAt  time.Time `json:"settled_at,omitzero"`
	Debtor     partyView `json:"debtor"`
	Creditor   partyView `json:"creditor"`
}

func (c *Control) transactions(w http.ResponseWriter, r *http.Request) {
	list, err := c.Transactions.Transactions(r.Context())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]transactionView, len(list))
	for i, t := range list {
		out[i] = transactionView{
			EndToEndID: t.ID, Amount: dict.FormatAmount(t.Amount), SettledAt: t.SettlementTime,
			Debtor:   partyView{Participant: t.DebtorParticipant, TaxID: t.DebtorTaxID},
			Creditor: partyView{Participant: t.CreditorParticipant, TaxID: t.CreditorTaxID, Key: t.CreditorKey},
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"transactions": out})
}

// addTransaction tells the DICT of a transaction settled in the SPI, so that it can be reported and refunded.
func (c *Control) addTransaction(w http.ResponseWriter, r *http.Request) {
	var v transactionView
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	amount, err := dict.ParseAmount(v.Amount)
	if err != nil {
		fail(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	err = c.Transactions.AddTransaction(r.Context(), dict.Transaction{
		ID: v.EndToEndID, Amount: amount, SettlementTime: v.SettledAt,
		DebtorParticipant: v.Debtor.Participant, DebtorTaxID: v.Debtor.TaxID,
		CreditorParticipant: v.Creditor.Participant, CreditorTaxID: v.Creditor.TaxID, CreditorKey: v.Creditor.Key,
	})
	var de *dict.Error
	switch {
	case errors.As(err, &de):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": de.Detail, "violations": de.Violations})
	case err != nil:
		fail(w, http.StatusInternalServerError, err.Error())
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
