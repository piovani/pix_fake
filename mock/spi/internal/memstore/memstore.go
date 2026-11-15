// Package memstore is an in-memory spi.Store, used only in tests.
package memstore

import (
	"context"
	"sync"
	"time"

	"pixfake/mock/spi/internal/pix"
	"pixfake/mock/spi/internal/spi"
)

type stream struct {
	ispb   string
	cursor int64
}

type Store struct {
	mu       sync.Mutex
	seq      int64
	inbound  []spi.Message
	outbound []spi.Message
	streams  map[string]stream
	acked    map[string]int64
	payments map[string]pix.Payment
	accounts map[string]pix.Account
}

func New() *Store {
	return &Store{streams: map[string]stream{}, acked: map[string]int64{}}
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

func (s *Store) StartStream(_ context.Context, ispb, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.streams[token] = stream{ispb: ispb, cursor: s.acked[ispb]}
	return nil
}

func (s *Store) Read(_ context.Context, ispb, token, newToken string, limit int) ([]spi.Message, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[token]
	if !ok || st.ispb != ispb {
		return nil, "", spi.ErrStreamGone
	}
	var msgs []spi.Message
	for _, m := range s.outbound {
		if m.ISPB == ispb && m.Seq > st.cursor {
			msgs = append(msgs, m)
			if len(msgs) == limit {
				break
			}
		}
	}
	if len(msgs) == 0 {
		return nil, token, nil
	}
	s.streams[newToken] = stream{ispb: ispb, cursor: msgs[len(msgs)-1].Seq}
	return msgs, newToken, nil
}

func (s *Store) Ack(_ context.Context, ispb, token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.streams[token]
	if !ok || st.ispb != ispb {
		return spi.ErrStreamGone
	}
	delete(s.streams, token)
	if st.cursor > s.acked[ispb] {
		s.acked[ispb] = st.cursor
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
	if existing, ok := s.payments[p.EndToEndID]; ok {
		return existing, false, nil
	}
	s.payments[p.EndToEndID] = p
	return p, true, nil
}

func (s *Store) GetPayment(_ context.Context, endToEndID string) (pix.Payment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[endToEndID]
	if !ok {
		return pix.Payment{}, pix.ErrPaymentNotFound
	}
	return p, nil
}

func (s *Store) FinalizePayment(_ context.Context, endToEndID, status, reasonCode, reasonDetail string, at time.Time) (pix.Payment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.payments[endToEndID]
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
	s.payments[endToEndID] = p
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
