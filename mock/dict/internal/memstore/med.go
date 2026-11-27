package memstore

import (
	"context"
	"sort"
	"time"

	"pixfake/mock/dict/internal/dict"
)

type med struct {
	transactions map[string]dict.Transaction
	reports      map[string]dict.InfractionReport
	markers      map[string]dict.FraudMarker
	refunds      map[string]dict.Refund
	recoveries   map[string]dict.FundsRecovery
	events       []dict.Event
}

func newMED() med {
	return med{
		transactions: map[string]dict.Transaction{}, reports: map[string]dict.InfractionReport{},
		markers: map[string]dict.FraudMarker{}, refunds: map[string]dict.Refund{}, recoveries: map[string]dict.FundsRecovery{},
	}
}

// tx keeps its writes apart until the function of InTx returns nil. The store's lock is held all along, which is
// what serializes transactions here.
type tx struct {
	s          *Store
	reports    map[string]dict.InfractionReport
	markers    map[string]dict.FraudMarker
	refunds    map[string]dict.Refund
	recoveries map[string]dict.FundsRecovery
	events     []dict.Event
}

func (s *Store) InTx(_ context.Context, fn func(dict.MEDTx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &tx{
		s: s, reports: map[string]dict.InfractionReport{}, markers: map[string]dict.FraudMarker{}, refunds: map[string]dict.Refund{},
		recoveries: map[string]dict.FundsRecovery{},
	}
	if err := fn(t); err != nil {
		return err
	}
	for k, v := range t.reports {
		s.med.reports[k] = v
	}
	for k, v := range t.markers {
		s.med.markers[k] = v
	}
	for k, v := range t.refunds {
		s.med.refunds[k] = v
	}
	for k, v := range t.recoveries {
		s.med.recoveries[k] = v
	}
	for _, e := range t.events {
		e.ID = int64(len(s.med.events)) + 1
		s.med.events = append(s.med.events, e)
	}
	return nil
}

func (t *tx) Transaction(_ context.Context, id string) (dict.Transaction, error) {
	tr, ok := t.s.med.transactions[id]
	if !ok {
		return dict.Transaction{}, dict.ErrNotFound
	}
	return tr, nil
}

func lookup[T any](pending, stored map[string]T, id string) (T, error) {
	if v, ok := pending[id]; ok {
		return v, nil
	}
	if v, ok := stored[id]; ok {
		return v, nil
	}
	var zero T
	return zero, dict.ErrNotFound
}

// merged is the stored records with the pending ones over them.
func merged[T any](pending, stored map[string]T) map[string]T {
	out := make(map[string]T, len(stored)+len(pending))
	for k, v := range stored {
		out[k] = v
	}
	for k, v := range pending {
		out[k] = v
	}
	return out
}

func (t *tx) Report(_ context.Context, id string) (dict.InfractionReport, error) {
	return lookup(t.reports, t.s.med.reports, id)
}

func (t *tx) ReportsOf(_ context.Context, transactionID string) ([]dict.InfractionReport, error) {
	var out []dict.InfractionReport
	for _, r := range merged(t.reports, t.s.med.reports) {
		if r.TransactionID == transactionID {
			out = append(out, r)
		}
	}
	sortByModified(out, func(r dict.InfractionReport) (time.Time, string) { return r.Modified, r.ID })
	return out, nil
}

func (t *tx) SaveReport(_ context.Context, r dict.InfractionReport) error {
	t.reports[r.ID] = r
	return nil
}

func (t *tx) Marker(_ context.Context, id string) (dict.FraudMarker, error) {
	return lookup(t.markers, t.s.med.markers, id)
}

func (t *tx) MarkerByRequestID(_ context.Context, participant, requestID string) (dict.FraudMarker, error) {
	for _, m := range merged(t.markers, t.s.med.markers) {
		if m.Registrant == participant && m.RequestID == requestID && requestID != "" {
			return m, nil
		}
	}
	return dict.FraudMarker{}, dict.ErrNotFound
}

func (t *tx) SaveMarker(_ context.Context, m dict.FraudMarker) error {
	t.markers[m.ID] = m
	return nil
}

func (t *tx) Refund(_ context.Context, id string) (dict.Refund, error) {
	return lookup(t.refunds, t.s.med.refunds, id)
}

func (t *tx) RefundsOf(_ context.Context, transactionID string) ([]dict.Refund, error) {
	var out []dict.Refund
	for _, r := range merged(t.refunds, t.s.med.refunds) {
		if r.TransactionID == transactionID {
			out = append(out, r)
		}
	}
	sortByModified(out, func(r dict.Refund) (time.Time, string) { return r.Modified, r.ID })
	return out, nil
}

func (t *tx) SaveRefund(_ context.Context, r dict.Refund) error {
	t.refunds[r.ID] = r
	return nil
}

func (t *tx) TransactionsFrom(_ context.Context, participant, taxID string) ([]dict.Transaction, error) {
	var out []dict.Transaction
	for _, tr := range t.s.med.transactions {
		if tr.DebtorParticipant == participant && tr.DebtorTaxID == taxID {
			out = append(out, tr)
		}
	}
	sortByModified(out, func(tr dict.Transaction) (time.Time, string) { return tr.SettlementTime, tr.ID })
	return out, nil
}

func (t *tx) FundsRecovery(_ context.Context, id string) (dict.FundsRecovery, error) {
	return lookup(t.recoveries, t.s.med.recoveries, id)
}

func (t *tx) FundsRecoveryOf(_ context.Context, rootTransactionID string) (dict.FundsRecovery, error) {
	for _, f := range merged(t.recoveries, t.s.med.recoveries) {
		if f.RootTransactionID == rootTransactionID {
			return f, nil
		}
	}
	return dict.FundsRecovery{}, dict.ErrNotFound
}

func (t *tx) SaveFundsRecovery(_ context.Context, f dict.FundsRecovery) error {
	t.recoveries[f.ID] = f
	return nil
}

// The store's lock serializes every transaction, so lock changes nothing here.

func (t *tx) ReportsOfRecovery(_ context.Context, recoveryID string, _ bool) ([]dict.InfractionReport, error) {
	var out []dict.InfractionReport
	for _, r := range merged(t.reports, t.s.med.reports) {
		if r.FundsRecoveryID == recoveryID {
			out = append(out, r)
		}
	}
	sortByModified(out, func(r dict.InfractionReport) (time.Time, string) { return r.Created, r.ID })
	return out, nil
}

func (t *tx) RefundsOfRecovery(_ context.Context, recoveryID string, _ bool) ([]dict.Refund, error) {
	var out []dict.Refund
	for _, r := range merged(t.refunds, t.s.med.refunds) {
		if r.FundsRecoveryID == recoveryID {
			out = append(out, r)
		}
	}
	sortByModified(out, func(r dict.Refund) (time.Time, string) { return r.Created, r.ID })
	return out, nil
}

func (t *tx) AddEvent(_ context.Context, e dict.Event) error {
	t.events = append(t.events, e)
	return nil
}

func (s *Store) ListEvents(_ context.Context, participant string, afterID int64, since time.Time, limit int) ([]dict.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.Event
	for _, e := range s.med.events {
		if e.ID > afterID && e.Participant == participant && !e.Time.Before(since) {
			if len(out) == limit {
				break
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func sortByModified[T any](list []T, key func(T) (time.Time, string)) {
	sort.Slice(list, func(i, j int) bool {
		ti, ii := key(list[i])
		tj, ij := key(list[j])
		if !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return ii < ij
	})
}

func (s *Store) AddTransaction(_ context.Context, t dict.Transaction) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.med.transactions[t.ID] = t
	return nil
}

func (s *Store) ListTransactions(_ context.Context, q dict.TransactionQuery) ([]dict.Transaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.Transaction
	for _, t := range s.med.transactions {
		if (q.CreditorTaxID == "" || t.CreditorTaxID == q.CreditorTaxID) && (q.CreditorKey == "" || t.CreditorKey == q.CreditorKey) {
			out = append(out, t)
		}
	}
	sortByModified(out, func(t dict.Transaction) (time.Time, string) { return t.SettlementTime, t.ID })
	return out, nil
}

func (s *Store) ListReports(_ context.Context, q dict.ReportQuery) ([]dict.InfractionReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.InfractionReport
	for _, r := range s.med.reports {
		if (q.Participant == "" || r.Reporter == q.Participant || r.Counterparty == q.Participant) &&
			(q.CreditorTaxID == "" || r.CreditorTaxID == q.CreditorTaxID) && (q.CreditorKey == "" || r.CreditorKey == q.CreditorKey) {
			out = append(out, r)
		}
	}
	sortByModified(out, func(r dict.InfractionReport) (time.Time, string) { return r.Modified, r.ID })
	return out, nil
}

func (s *Store) ListMarkers(_ context.Context, q dict.MarkerQuery) ([]dict.FraudMarker, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.FraudMarker
	for _, m := range s.med.markers {
		if (q.TaxID == "" || m.TaxID == q.TaxID) && (q.Key == "" || m.Key == q.Key) {
			out = append(out, m)
		}
	}
	sortByModified(out, func(m dict.FraudMarker) (time.Time, string) { return m.Modified, m.ID })
	return out, nil
}

func (s *Store) ListRefunds(_ context.Context, participant string) ([]dict.Refund, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.Refund
	for _, r := range s.med.refunds {
		if r.Requesting == participant || r.Contested == participant {
			out = append(out, r)
		}
	}
	sortByModified(out, func(r dict.Refund) (time.Time, string) { return r.Modified, r.ID })
	return out, nil
}

func (s *Store) EntriesOf(_ context.Context, taxID string) ([]dict.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.Entry
	for _, e := range s.entries {
		if e.Owner.TaxID == taxID {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}
