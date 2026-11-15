package spi

import (
	"context"
	"time"
)

// Store keeps the messages and how far each participant has read.
type Store interface {
	// AddInbound stores messages sent by a participant, atomically.
	AddInbound(ctx context.Context, msgs []Message) error
	// AddOutbound stores a message to be read by the participant in m.ISPB.
	AddOutbound(ctx context.Context, m Message) error
	// ReadAfter returns up to limit messages for the participant with Seq greater than after, in order.
	ReadAfter(ctx context.Context, ispb string, after int64, limit int) ([]Message, error)
	// Acked returns the position the participant has acknowledged, 0 if none.
	Acked(ctx context.Context, ispb string) (int64, error)
	// PurgeMessages deletes the messages created before t that nobody needs any more: those a participant
	// has acknowledged, and the ones participants sent. It returns how many it deleted.
	PurgeMessages(ctx context.Context, before time.Time) (int64, error)
	// Ack records that the participant has read up to seq. It never moves back.
	Ack(ctx context.Context, ispb string, seq int64) error
}
