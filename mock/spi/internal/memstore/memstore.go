// Package memstore is an in-memory spi.Store, used only in tests.
package memstore

import (
	"context"
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
