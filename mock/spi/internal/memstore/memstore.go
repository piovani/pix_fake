// Package memstore is an in-memory spi.Store, used only in tests.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/spi"
)

type Store struct {
	mu       sync.Mutex
	seq      int64
	inbound  []spi.Message
	outbound []spi.Message
	acked    map[string]int64
	payments map[string]pix.Payment
	accounts map[string]pix.Account
	reported map[string]pix.ReportedTransaction
}

func New() *Store {
	return &Store{acked: map[string]int64{}}
}

// Inbound returns the messages received from participants, for assertions in tests.
func (s *Store) Inbound() []spi.Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]spi.Message(nil), s.inbound...)
}

func (s *Store) AddInbound(_ context.Context, msgs []spi.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		s.seq++
		m.Seq = s.seq
		s.inbound = append(s.inbound, m)
	}
	return nil
}

func (s *Store) AddOutbound(_ context.Context, m spi.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	m.Seq = s.seq
	s.outbound = append(s.outbound, m)
	return nil
}

func (s *Store) ReadAfter(_ context.Context, ispb string, after int64, limit int) ([]spi.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var msgs []spi.Message
	for _, m := range s.outbound {
		if m.ISPB == ispb && m.Seq > after {
			msgs = append(msgs, m)
			if len(msgs) == limit {
				break
			}
		}
	}
	return msgs, nil
}

func (s *Store) Acked(_ context.Context, ispb string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.acked[ispb], nil
}

func (s *Store) PurgeMessages(_ context.Context, before time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var deleted int64
	keep := s.outbound[:0]
	for _, m := range s.outbound {
		if m.CreatedAt.Before(before) && m.Seq <= s.acked[m.ISPB] {
			deleted++
			continue
		}
		keep = append(keep, m)
	}
	s.outbound = keep
	keepIn := s.inbound[:0]
	for _, m := range s.inbound {
		if m.CreatedAt.Before(before) {
			deleted++
			continue
		}
		keepIn = append(keepIn, m)
	}
	s.inbound = keepIn
	return deleted, nil
}

func (s *Store) Ack(_ context.Context, ispb string, seq int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if seq > s.acked[ispb] {
		s.acked[ispb] = seq
	}
	return nil
}

// --- Payments ---

var _ pix.PaymentStore = (*Store)(nil)

func (s *Store) CreatePayment(_ context.Context, p pix.Payment) (pix.Payment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.payments == nil {
		s.payments = map[string]pix.Payment{}
	}
	if existing, ok := s.payments[p.ID]; ok {
		return existing, false, nil
	}
	s.payments[p.ID] = p
	return p, true, nil
}

func (s *Store) GetPayment(_ context.Context, id string) (pix.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[id]
	if !ok {
		return pix.Payment{}, pix.ErrPaymentNotFound
	}
	return p, nil
}

func (s *Store) ReturnsOf(_ context.Context, endToEndID string) ([]pix.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []pix.Payment
	for _, p := range s.payments {
		if p.Kind == pix.KindReturn && p.EndToEndID == endToEndID {
			out = append(out, p)
		}
	}
	return out, nil
}

func (s *Store) PendingBefore(_ context.Context, t time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ids []string
	for id, p := range s.payments {
		if p.Status == pix.StatusPending && !p.CreatedAt.After(t) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

func (s *Store) FinalizePayment(_ context.Context, id, status, reasonCode, reasonDetail string, at time.Time) (pix.Payment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[id]
	if !ok {
		return pix.Payment{}, false, pix.ErrPaymentNotFound
	}
	if p.Status != pix.StatusPending {
		return p, false, nil
	}
	p.Status, p.ReasonCode, p.ReasonDetail = status, reasonCode, reasonDetail
	if status == pix.StatusSettled {
		p.SettledAt = at
	}
	s.payments[id] = p
	return p, true, nil
}

// --- Accounts ---

var _ pix.AccountStore = (*Store)(nil)

func (s *Store) CreateAccount(_ context.Context, a pix.Account) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.accounts == nil {
		s.accounts = map[string]pix.Account{}
	}
	id := a.ISPB + "/" + a.Number
	if _, ok := s.accounts[id]; ok {
		return false, nil
	}
	s.accounts[id] = a
	return true, nil
}

func (s *Store) GetAccount(_ context.Context, ispb, number string) (pix.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[ispb+"/"+number]
	if !ok {
		return pix.Account{}, pix.ErrAccountNotFound
	}
	return a, nil
}

// --- Reported transactions ---

var _ pix.ReportStore = (*Store)(nil)

func (s *Store) AddReported(_ context.Context, t pix.ReportedTransaction) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reported == nil {
		s.reported = map[string]pix.ReportedTransaction{}
	}
	if _, ok := s.reported[t.ID]; ok {
		return false, nil
	}
	s.reported[t.ID] = t
	return true, nil
}

func (s *Store) GetReported(_ context.Context, id string) (pix.ReportedTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.reported[id]
	if !ok {
		return pix.ReportedTransaction{}, pix.ErrReportedNotFound
	}
	return t, nil
}

// --- Inspection (control API) ---

var _ pix.Inspector = (*Store)(nil)

func (s *Store) ListPayments(_ context.Context, f pix.PaymentFilter) ([]pix.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []pix.Payment
	for _, p := range s.payments {
		if (f.Status == "" || p.Status == f.Status) && (f.Kind == "" || p.Kind == f.Kind) &&
			(f.Participant == "" || p.PayerISPB == f.Participant || p.PayeeISPB == f.Participant) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if limit := pix.ClampLimit(f.Limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ListMessages(_ context.Context, f pix.MessageFilter) ([]pix.StoredMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []pix.StoredMessage
	add := func(msgs []spi.Message, direction string) {
		for _, m := range msgs {
			if (f.Direction == "" || f.Direction == direction) && (f.Participant == "" || m.ISPB == f.Participant) {
				out = append(out, pix.StoredMessage{Seq: m.Seq, ResourceID: m.ResourceID, Participant: m.ISPB, Direction: direction,
					CreatedAt: m.CreatedAt, Body: string(m.Body)})
			}
		}
	}
	add(s.inbound, "in")
	add(s.outbound, "out")
	sort.Slice(out, func(i, j int) bool { return out[i].Seq > out[j].Seq })
	if limit := pix.ClampLimit(f.Limit); len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ListAccounts(_ context.Context) ([]pix.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]pix.Account, 0, len(s.accounts))
	for _, a := range s.accounts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ISPB != out[j].ISPB {
			return out[i].ISPB < out[j].ISPB
		}
		return out[i].Number < out[j].Number
	})
	return out, nil
}

// Reset keeps the accounts and the sequence of messages, so a stream token a client still holds stays meaningful.
func (s *Store) Reset(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inbound, s.outbound = nil, nil
	s.acked = map[string]int64{}
	s.payments = nil
	s.reported = nil
	return nil
}
