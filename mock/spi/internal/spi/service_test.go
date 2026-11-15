package spi_test

import (
	"context"
	"testing"
	"time"

	"pixfake/mock/spi/internal/memstore"
	"pixfake/mock/spi/internal/spi"
)

func TestRunRetentionDeletesWhatWasRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := spi.NewService(memstore.New(), 0)

	if _, err := svc.Deliver(ctx, "11111111", []byte("<m>read</m>")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Deliver(ctx, "11111111", []byte("<m>unread</m>")); err != nil {
		t.Fatal(err)
	}
	token, _ := svc.StartStream(ctx, "11111111")
	_, next, err := svc.Pull(ctx, "11111111", token, 1) // reads the first one only
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseStream(ctx, "11111111", next); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond) // so the messages are older than the retention below

	deleted := make(chan int64, 4)
	go svc.RunRetention(ctx, 5*time.Millisecond, time.Millisecond, func(n int64, err error) {
		if err != nil {
			t.Error(err)
		}
		if n > 0 {
			deleted <- n
		}
	})
	select {
	case n := <-deleted:
		if n != 1 {
			t.Fatalf("deleted %d messages, want only the one that was read", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing was deleted")
	}

	fresh, _ := svc.StartStream(ctx, "11111111")
	msgs, _, err := svc.Pull(ctx, "11111111", fresh, 10)
	if err != nil || len(msgs) != 1 || string(msgs[0].Body) != "<m>unread</m>" {
		t.Fatalf("messages left = %d, %v; the unread one must stay", len(msgs), err)
	}
}
