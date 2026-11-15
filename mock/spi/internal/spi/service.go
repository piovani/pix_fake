package spi

import (
	"context"
	"sync"
	"time"
)

// Service implements the message exchange of the ICOM interface.
type Service struct {
	store    Store
	pollWait time.Duration
	notify   *notifier
	now      func() time.Time
}

// NewService creates the service. pollWait is how long a read waits for a message
// before answering that there is none (long polling).
func NewService(store Store, pollWait time.Duration) *Service {
	return &Service{store: store, pollWait: pollWait, notify: newNotifier(), now: time.Now}
}

// Receive stores messages sent by a participant and returns their PI-ResourceIds, in order.
// Like the real API, it does not validate the content here.
func (s *Service) Receive(ctx context.Context, ispb string, bodies [][]byte) ([]string, error) {
	msgs := make([]Message, len(bodies))
	ids := make([]string, len(bodies))
	for i, b := range bodies {
		ids[i] = NewResourceID()
		msgs[i] = Message{ResourceID: ids[i], ISPB: ispb, Body: b, CreatedAt: s.now()}
	}
	if err := s.store.AddInbound(ctx, msgs); err != nil {
		return nil, err
	}
	return ids, nil
}

// Deliver queues a message for the participant to read from its output stream.
func (s *Service) Deliver(ctx context.Context, ispb string, body []byte) (string, error) {
	m := Message{ResourceID: NewResourceID(), ISPB: ispb, Body: body, CreatedAt: s.now()}
	if err := s.store.AddOutbound(ctx, m); err != nil {
		return "", err
	}
	s.notify.broadcast(ispb)
	return m.ResourceID, nil
}

// StartStream opens a new read stream and returns its token.
func (s *Service) StartStream(ctx context.Context, ispb string) (string, error) {
	token := newToken()
	if err := s.store.StartStream(ctx, ispb, token); err != nil {
		return "", err
	}
	return token, nil
}

// Pull reads up to limit messages from the stream. With none available it waits up to
// the poll time, then returns no messages and the token to use next.
func (s *Service) Pull(ctx context.Context, ispb, token string, limit int) ([]Message, string, error) {
	if limit < 1 || limit > MaxMessages {
		limit = 1
	}
	// Subscribe before reading so a message delivered in between is not missed.
	wake := s.notify.channel(ispb)
	msgs, next, err := s.store.Read(ctx, ispb, token, newToken(), limit)
	if err != nil || len(msgs) > 0 || s.pollWait <= 0 {
		return msgs, next, err
	}
	timer := time.NewTimer(s.pollWait)
	defer timer.Stop()
	select {
	case <-wake:
		return s.store.Read(ctx, ispb, token, newToken(), limit)
	case <-timer.C:
		return nil, next, nil
	case <-ctx.Done():
		return nil, next, nil
	}
}

// CloseStream marks what the stream delivered as read and releases it.
func (s *Service) CloseStream(ctx context.Context, ispb, token string) error {
	return s.store.Ack(ctx, ispb, token)
}

// notifier wakes the reads waiting for messages of a participant.
type notifier struct {
	mu    sync.Mutex
	chans map[string]chan struct{}
}

func newNotifier() *notifier { return &notifier{chans: map[string]chan struct{}{}} }

func (n *notifier) channel(ispb string) <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	ch, ok := n.chans[ispb]
	if !ok {
		ch = make(chan struct{})
		n.chans[ispb] = ch
	}
	return ch
}

func (n *notifier) broadcast(ispb string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if ch, ok := n.chans[ispb]; ok {
		close(ch)
		delete(n.chans, ispb)
	}
}
