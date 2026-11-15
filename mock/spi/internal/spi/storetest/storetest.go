// Package storetest holds the behavior every spi.Store must have, so the in-memory and
// the Postgres implementations are tested by the same cases.
package storetest

import (
	"context"
	"testing"

	"pixfake/mock/spi/internal/spi"
)

const (
	a = "11111111"
	b = "22222222"
)

func out(id, ispb string) spi.Message {
	return spi.Message{ResourceID: id, ISPB: ispb, Body: []byte("<m>" + id + "</m>")}
}

func ids(msgs []spi.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ResourceID
	}
	return out
}

// Run executes the cases. newStore must return an empty store for each call.
func Run(t *testing.T, newStore func(t *testing.T) spi.Store) {
	ctx := context.Background()

	t.Run("read in order, limited, after a position", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"m1", "m2", "m3"} {
			if err := s.AddOutbound(ctx, out(id, a)); err != nil {
				t.Fatal(err)
			}
		}
		first, err := s.ReadAfter(ctx, a, 0, 2)
		if err != nil || len(first) != 2 || first[0].ResourceID != "m1" || first[1].ResourceID != "m2" || first[0].Seq >= first[1].Seq {
			t.Fatalf("first read = %v, %v", ids(first), err)
		}
		rest, err := s.ReadAfter(ctx, a, first[1].Seq, 2)
		if err != nil || len(rest) != 1 || rest[0].ResourceID != "m3" {
			t.Fatalf("read after the second = %v, %v", ids(rest), err)
		}
		none, err := s.ReadAfter(ctx, a, rest[0].Seq, 2)
		if err != nil || len(none) != 0 {
			t.Fatalf("read at the end = %v, %v", ids(none), err)
		}
	})

	t.Run("only the messages of the participant, and only outbound ones", func(t *testing.T) {
		s := newStore(t)
		_ = s.AddOutbound(ctx, out("for-b", b))
		_ = s.AddInbound(ctx, []spi.Message{{ResourceID: "from-a", ISPB: a, Body: []byte("x")}})
		msgs, err := s.ReadAfter(ctx, a, 0, 10)
		if err != nil || len(msgs) != 0 {
			t.Fatalf("read = %v, %v; want nothing", ids(msgs), err)
		}
	})

	t.Run("ack moves forward only", func(t *testing.T) {
		s := newStore(t)
		if got, err := s.Acked(ctx, a); err != nil || got != 0 {
			t.Fatalf("acked at the start = %d, %v", got, err)
		}
		if err := s.Ack(ctx, a, 5); err != nil {
			t.Fatal(err)
		}
		if err := s.Ack(ctx, a, 3); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Acked(ctx, a); got != 5 {
			t.Fatalf("acked = %d, want 5 (an ack never goes back)", got)
		}
		if got, _ := s.Acked(ctx, b); got != 0 {
			t.Fatalf("acked of another participant = %d, want 0", got)
		}
	})
}
