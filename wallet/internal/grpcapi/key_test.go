package grpcapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
)

type fakeKeys struct {
	got pixkey.RegisterInput
	key pixkey.Key
	err error
}

func (f *fakeKeys) Register(_ context.Context, in pixkey.RegisterInput) (pixkey.Key, error) {
	f.got = in
	return f.key, f.err
}

func TestRegisterKey(t *testing.T) {
	created := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	f := &fakeKeys{key: pixkey.Key{Key: "ana.souza@example.com", Type: pix.KeyEmail, AccountID: "acc", CreatedAt: created}}
	client := walletv1.NewKeyServiceClient(dialWith(t, fakeLedger{}, f))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.RegisterKey(ctx, &walletv1.RegisterKeyRequest{
		AccountId: "acc", Type: walletv1.KeyType_KEY_TYPE_EMAIL, Key: "ana.souza@example.com", RequestId: "req",
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.got.AccountID != "acc" || f.got.Type != pix.KeyEmail || f.got.Key != "ana.souza@example.com" || f.got.RequestID != "req" {
		t.Fatalf("input: %+v", f.got)
	}
	k := res.GetKey()
	if k.GetKey() != "ana.souza@example.com" || k.GetType() != walletv1.KeyType_KEY_TYPE_EMAIL || k.GetAccountId() != "acc" || !k.GetCreatedAt().AsTime().Equal(created) {
		t.Fatalf("key: %v", k)
	}
}

func TestRegisterKeyErrors(t *testing.T) {
	tests := []struct {
		err  error
		want codes.Code
	}{
		{pixkey.ErrInvalid, codes.InvalidArgument},
		{ledger.ErrAccountNotFound, codes.NotFound},
		{pixkey.ErrKeyTaken, codes.AlreadyExists},
		{pixkey.ErrRequestConflict, codes.AlreadyExists},
		{&pix.APIError{Status: 429}, codes.ResourceExhausted},
		{&pix.APIError{Status: 503}, codes.Unavailable},
		{errors.New("connection refused to 10.0.0.5"), codes.Internal},
	}
	for _, tt := range tests {
		client := walletv1.NewKeyServiceClient(dialWith(t, fakeLedger{}, &fakeKeys{err: tt.err}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := client.RegisterKey(ctx, &walletv1.RegisterKeyRequest{AccountId: "acc"})
		cancel()
		if status.Code(err) != tt.want || strings.Contains(err.Error(), "10.0.0.5") {
			t.Errorf("%v: want %v, got %v", tt.err, tt.want, err)
		}
	}
}

func TestKeyServiceIsOptionalAndTheOtherCallsAreNotBuilt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := walletv1.NewKeyServiceClient(dial(t, fakeLedger{})).RegisterKey(ctx, &walletv1.RegisterKeyRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("service not registered: %v", err)
	}
	client := walletv1.NewKeyServiceClient(dialWith(t, fakeLedger{}, &fakeKeys{}))
	if _, err := client.ListKeys(ctx, &walletv1.ListKeysRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("ListKeys: %v", err)
	}
}
