// Package memstore is an in-memory dict.Repository, used only in tests.
package memstore

import (
	"context"
	"sort"
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

// DeleteAll removes every entry (control API).
func (s *Store) DeleteAll(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = map[string]dict.Entry{}
	s.requestID = map[string]string{}
	return nil
}

// ListEntries returns the entries, those of one participant when it is not empty, ordered by key (control API).
func (s *Store) ListEntries(_ context.Context, participant string, limit int) ([]dict.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []dict.Entry
	for _, e := range s.entries {
		if participant == "" || e.Account.Participant == participant {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
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
