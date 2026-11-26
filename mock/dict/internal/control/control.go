// Package control is the control API of the DICT mock, under /mock. It is not part of the Bacen contract: it is how
// a test looks at the registered keys, puts the mock back to its seed data and makes the API misbehave. It speaks
// JSON, to be easy to call from a script.
//
//	GET    /mock/entries[?participant=&limit=]   the registered keys, ordered by key
//	POST   /mock/reset                           removes every entry and loads the seed data again
//	GET    /mock/faults, POST /mock/faults, DELETE /mock/faults[/{id}]   make the API answer 429/503 or late
package control

import (
	"context"
	"encoding/json"
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

// Control serves the control API.
type Control struct {
	Store Store
	// Reseed loads the seed data again; it runs after the entries are removed.
	Reseed func(ctx context.Context) error
	Faults *fault.Set
}

// New creates the control API. faults is the set the API consults; when nil the control API gets one of its own.
func New(store Store, reseed func(ctx context.Context) error, faults *fault.Set) *Control {
	if faults == nil {
		faults = &fault.Set{}
	}
	return &Control{Store: store, Reseed: reseed, Faults: faults}
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
