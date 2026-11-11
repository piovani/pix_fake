// Package memstore is an in-memory dict.Repository, used only in tests.
package memstore

import (
	"context"
	"sync"

	"pixfake/mock/dict/internal/dict"
)

type Store struct {
	mu        sync.Mutex
	entries   map[string]dict.Entry
	requestID map[string]string // "participant/requestID" -> key
}

func New() *Store {
	return &Store{entries: map[string]dict.Entry{}, requestID: map[string]string{}}
}

func rid(participant, requestID string) string { return participant + "/" + requestID }

func (s *Store) Create(_ context.Context, e dict.Entry, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.requestID[rid(e.Account.Participant, requestID)]; ok {
		return dict.ErrRequestIDUsed
	}
	if _, ok := s.entries[e.Key]; ok {
		return dict.ErrKeyExists
	}
	s.entries[e.Key] = e
	s.requestID[rid(e.Account.Participant, requestID)] = e.Key
	return nil
}

func (s *Store) Get(_ context.Context, key string) (dict.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return dict.Entry{}, dict.ErrNotFound
	}
	return e, nil
}

func (s *Store) GetByRequestID(_ context.Context, participant, requestID string) (dict.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key, ok := s.requestID[rid(participant, requestID)]
	if !ok {
		return dict.Entry{}, dict.ErrNotFound
	}
	e, ok := s.entries[key]
	if !ok {
		return dict.Entry{}, dict.ErrNotFound
	}
	return e, nil
}

func (s *Store) Update(_ context.Context, e dict.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[e.Key]; !ok {
		return dict.ErrNotFound
	}
	s.entries[e.Key] = e
	return nil
}

func (s *Store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.entries[key]; !ok {
		return dict.ErrNotFound
	}
	delete(s.entries, key)
	return nil
}

func (s *Store) Existing(_ context.Context, keys []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		_, out[k] = s.entries[k]
	}
	return out, nil
}
