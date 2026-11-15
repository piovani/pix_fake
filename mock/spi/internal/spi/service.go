package spi

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// Handler processes the messages participants send. The payment logic implements it.
type Handler interface {
	// Handle is called, in order, for each message after it was stored. Problems with the
	// content are the handler's to report; they do not change the answer to the sender.
	Handle(ctx context.Context, sender string, m Message)
}

// Service implements the message exchange of the ICOM interface.
type Service struct {
	store    Store
	pollWait time.Duration
	notify   *notifier
	now      func() time.Time
	handler  Handler
}

// NewService creates the service. pollWait is how long a read waits for a message
// before answering that there is none (long polling).
func NewService(store Store, pollWait time.Duration) *Service {
	return &Service{store: store, pollWait: pollWait, notify: newNotifier(), now: time.Now}
}

// SetHandler sets what processes the received messages. Call it before serving requests.
func (s *Service) SetHandler(h Handler) { s.handler = h }

// Receive stores messages sent by a participant and returns their PI-ResourceIds, in order.
// Like the real API, it does not validate the content here: it hands the stored messages
// to the handler, which processes them (the SPI does that after answering 201).
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
	if s.handler != nil {
		// Processing goes on even if the sender gives up waiting for the answer.
		hctx := context.WithoutCancel(ctx)
		for _, m := range msgs {
			s.handler.Handle(hctx, ispb, m)
		}
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

// A stream token is the position in the participant's queue, in hex. The stream is stateless:
// a token that is older than what the participant already acknowledged is "gone" (410).

func encodeToken(seq int64) string { return strconv.FormatInt(seq, 16) }

func decodeToken(token string) (int64, error) {
	seq, err := strconv.ParseInt(token, 16, 64)
	if err != nil || seq < 0 {
		return 0, ErrStreamGone
	}
	return seq, nil
}

// checkToken returns the position of a token, or ErrStreamGone when it is malformed or stale.
func (s *Service) checkToken(ctx context.Context, ispb, token string) (int64, error) {
	pos, err := decodeToken(token)
	if err != nil {
		return 0, err
	}
	acked, err := s.store.Acked(ctx, ispb)
	if err != nil {
		return 0, err
	}
	if pos < acked {
		return 0, ErrStreamGone
	}
	return pos, nil
}

// StartStream opens a read stream, positioned after the last message the participant acknowledged,
// and returns its token.
func (s *Service) StartStream(ctx context.Context, ispb string) (string, error) {
	acked, err := s.store.Acked(ctx, ispb)
	if err != nil {
		return "", err
	}
	return encodeToken(acked), nil
}

// Pull reads up to limit messages after the position of the token. With none available it waits
// up to the poll time, then returns no messages and the token to use next.
func (s *Service) Pull(ctx context.Context, ispb, token string, limit int) ([]Message, string, error) {
	pos, err := s.checkToken(ctx, ispb, token)
	if err != nil {
		return nil, "", err
	}
	if limit < 1 || limit > MaxMessages {
		limit = 1
	}
	// Subscribe before reading so a message delivered in between is not missed.
	wake := s.notify.channel(ispb)
	msgs, err := s.store.ReadAfter(ctx, ispb, pos, limit)
	if err == nil && len(msgs) == 0 && s.pollWait > 0 {
		timer := time.NewTimer(s.pollWait)
		defer timer.Stop()
		select {
		case <-wake:
			msgs, err = s.store.ReadAfter(ctx, ispb, pos, limit)
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	if err != nil {
		return nil, "", err
	}
	if len(msgs) == 0 {
		return nil, token, nil
	}
	return msgs, encodeToken(msgs[len(msgs)-1].Seq), nil
}

// CloseStream marks everything up to the position of the token as read.
func (s *Service) CloseStream(ctx context.Context, ispb, token string) error {
	pos, err := s.checkToken(ctx, ispb, token)
	if err != nil {
		return err
	}
	return s.store.Ack(ctx, ispb, pos)
}

// RunRetention deletes, every interval until ctx is done, the messages older than keep that nobody needs any more
// (see Store.PurgeMessages). Without it the message table only grows.
func (s *Service) RunRetention(ctx context.Context, every, keep time.Duration, onPurge func(deleted int64, err error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			deleted, err := s.store.PurgeMessages(ctx, s.now().Add(-keep))
			if onPurge != nil {
				onPurge(deleted, err)
			}
		}
	}
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
