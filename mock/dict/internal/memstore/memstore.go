// Package memstore is an in-memory dict.Repository, used only in tests.
package memstore

import (
	"context"
	"sort"
	"sync"
	"time"

	"pixfake/mock/dict/internal/dict"
)

type Store struct {
	mu        sync.Mutex
	entries   map[string]dict.Entry
	requestID map[string]string          // "participant/requestID" -> key
	events    map[string][]dict.CIDEvent // "participant/keyType" -> oldest first
	files     []dict.CIDFile
	syncs     int64
}

func New() *Store {
	return &Store{entries: map[string]dict.Entry{}, requestID: map[string]string{}, events: map[string][]dict.CIDEvent{}}
}

func scope(participant string, kt dict.KeyType) string { return participant + "/" + string(kt) }

func (s *Store) event(e dict.Entry, typ dict.CIDEventType) {
	k := scope(e.Account.Participant, e.KeyType)
	s.events[k] = append(s.events[k], dict.CIDEvent{Type: typ, CID: e.CID, Timestamp: time.Now().UTC().Truncate(time.Millisecond)})
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
	e.RequestID = requestID
	s.entries[e.Key] = e
	s.requestID[rid(e.Account.Participant, requestID)] = e.Key
	s.event(e, dict.CIDAdded)
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
	old, ok := s.entries[e.Key]
	if !ok {
		return dict.ErrNotFound
	}
	e.RequestID = old.RequestID
	s.entries[e.Key] = e
	s.event(old, dict.CIDRemoved)
	s.event(e, dict.CIDAdded)
	return nil
}

func (s *Store) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.entries[key]
	if !ok {
		return dict.ErrNotFound
	}
	delete(s.entries, key)
	s.event(old, dict.CIDRemoved)
	return nil
}

// DeleteAll removes every entry (control API).
func (s *Store) DeleteAll(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = map[string]dict.Entry{}
	s.requestID = map[string]string{}
	s.events = map[string][]dict.CIDEvent{}
	s.files, s.syncs = nil, 0
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

func (s *Store) CIDs(_ context.Context, participant string, kt dict.KeyType) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.entries {
		if e.Account.Participant == participant && e.KeyType == kt {
			out = append(out, e.CID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func (s *Store) GetByCID(_ context.Context, cid string) (dict.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		if e.CID == cid {
			return e, nil
		}
	}
	return dict.Entry{}, dict.ErrNotFound
}

func (s *Store) CIDEvents(_ context.Context, participant string, kt dict.KeyType) ([]dict.CIDEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]dict.CIDEvent(nil), s.events[scope(participant, kt)]...), nil
}

func (s *Store) CreateSyncVerification(_ context.Context, v dict.SyncVerification) (dict.SyncVerification, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.syncs++
	v.ID = s.syncs
	return v, nil
}

func (s *Store) CreateCIDFile(_ context.Context, f dict.CIDFile) (dict.CIDFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f.ID = int64(len(s.files) + 1)
	s.files = append(s.files, f)
	return f, nil
}

func (s *Store) GetCIDFile(_ context.Context, id int64) (dict.CIDFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id < 1 || id > int64(len(s.files)) {
		return dict.CIDFile{}, dict.ErrNotFound
	}
	return s.files[id-1], nil
}
