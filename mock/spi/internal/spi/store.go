package spi

import "context"

// Store keeps the messages and the read position of each participant.
type Store interface {
	// AddInbound stores messages sent by a participant, atomically.
	AddInbound(ctx context.Context, msgs []Message) error
	// AddOutbound stores a message to be read by the participant in m.ISPB.
	AddOutbound(ctx context.Context, m Message) error
	// StartStream opens a stream for the participant, positioned after the last acknowledged message.
	StartStream(ctx context.Context, ispb, token string) error
	// Read returns up to limit messages after the stream position. When there are some, it
	// opens a stream at newToken, positioned after the last one, and returns it as next;
	// otherwise next is the token that was passed in. Unknown token: ErrStreamGone.
	Read(ctx context.Context, ispb, token, newToken string, limit int) (msgs []Message, next string, err error)
	// Ack closes the stream and marks everything delivered up to its position as read.
	// Unknown token: ErrStreamGone.
	Ack(ctx context.Context, ispb, token string) error
}
