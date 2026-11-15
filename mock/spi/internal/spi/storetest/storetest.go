// Package storetest holds the behavior every spi.Store must have, so the in-memory and
// the Postgres implementations are tested by the same cases.
package storetest

import (
	"context"
	"errors"
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

// Run executes the cases. newStore must return an empty store for each call.
func Run(t *testing.T, newStore func(t *testing.T) spi.Store) {
	ctx := context.Background()

	t.Run("read in order, limited, and advance", func(t *testing.T) {
		s := newStore(t)
		for _, id := range []string{"m1", "m2", "m3"} {
			if err := s.AddOutbound(ctx, out(id, a)); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.StartStream(ctx, a, "t0"); err != nil {
			t.Fatal(err)
		}
		msgs, next, err := s.Read(ctx, a, "t0", "t1", 2)
		if err != nil || len(msgs) != 2 || msgs[0].ResourceID != "m1" || msgs[1].ResourceID != "m2" || next != "t1" {
			t.Fatalf("first read = %v, %q, %v", ids(msgs), next, err)
		}
		msgs, next, err = s.Read(ctx, a, "t1", "t2", 2)
		if err != nil || len(msgs) != 1 || msgs[0].ResourceID != "m3" || next != "t2" {
			t.Fatalf("second read = %v, %q, %v", ids(msgs), next, err)
		}
		msgs, next, err = s.Read(ctx, a, "t2", "t3", 2)
		if err != nil || len(msgs) != 0 || next != "t2" {
			t.Fatalf("empty read = %v, %q, %v; want no messages and the same token", ids(msgs), next, err)
		}
	})

	t.Run("only the messages of the participant", func(t *testing.T) {
		s := newStore(t)
		_ = s.AddOutbound(ctx, out("for-b", b))
		_ = s.StartStream(ctx, a, "t0")
		msgs, _, err := s.Read(ctx, a, "t0", "t1", 10)
		if err != nil || len(msgs) != 0 {
			t.Fatalf("read = %v, %v; want nothing", ids(msgs), err)
		}
	})

	t.Run("old token reads the same messages again", func(t *testing.T) {
		s := newStore(t)
		_ = s.AddOutbound(ctx, out("m1", a))
		_ = s.StartStream(ctx, a, "t0")
		first, _, _ := s.Read(ctx, a, "t0", "t1", 1)
		again, _, err := s.Read(ctx, a, "t0", "t9", 1)
		if err != nil || len(first) != 1 || len(again) != 1 || first[0].ResourceID != again[0].ResourceID {
			t.Fatalf("first %v, again %v, err %v; want the same message", ids(first), ids(again), err)
		}
	})

	t.Run("without ack a new stream delivers again, with ack it does not", func(t *testing.T) {
		s := newStore(t)
		_ = s.AddOutbound(ctx, out("m1", a))
		_ = s.AddOutbound(ctx, out("m2", a))
		_ = s.StartStream(ctx, a, "t0")
		_, next, _ := s.Read(ctx, a, "t0", "t1", 1) // m1, not acknowledged

		_ = s.StartStream(ctx, a, "u0")
		msgs, _, _ := s.Read(ctx, a, "u0", "u1", 1)
		if len(msgs) != 1 || msgs[0].ResourceID != "m1" {
			t.Fatalf("new stream without ack = %v; want m1 again", ids(msgs))
		}

		if err := s.Ack(ctx, a, next); err != nil { // acknowledges m1
			t.Fatal(err)
		}
		_ = s.StartStream(ctx, a, "v0")
		msgs, _, _ = s.Read(ctx, a, "v0", "v1", 5)
		if len(msgs) != 1 || msgs[0].ResourceID != "m2" {
			t.Fatalf("new stream after ack = %v; want only m2", ids(msgs))
		}
	})

	t.Run("gone streams", func(t *testing.T) {
		s := newStore(t)
		_ = s.StartStream(ctx, a, "t0")
		if _, _, err := s.Read(ctx, a, "nope", "n1", 1); !errors.Is(err, spi.ErrStreamGone) {
			t.Fatalf("read unknown token: %v", err)
		}
		if _, _, err := s.Read(ctx, b, "t0", "n1", 1); !errors.Is(err, spi.ErrStreamGone) {
			t.Fatalf("read with another participant: %v", err)
		}
		if err := s.Ack(ctx, a, "t0"); err != nil {
			t.Fatal(err)
		}
		if err := s.Ack(ctx, a, "t0"); !errors.Is(err, spi.ErrStreamGone) {
			t.Fatalf("second ack: %v", err)
		}
	})
}

func ids(msgs []spi.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.ResourceID
	}
	return out
}
