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
	got     pixkey.RegisterInput
	key     pixkey.Key
	list    []pixkey.Key
	removed [2]string
	err     error
}

func (f *fakeKeys) List(_ context.Context, accountID string) ([]pixkey.Key, error) {
	f.got.AccountID = accountID
	return f.list, f.err
}

func (f *fakeKeys) Remove(_ context.Context, accountID, key string) error {
	f.removed = [2]string{accountID, key}
	return f.err
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

func TestListAndRemoveKeys(t *testing.T) {
	f := &fakeKeys{list: []pixkey.Key{
		{Key: "a@example.com", Type: pix.KeyEmail, AccountID: "acc"},
		{Key: "8f3c1a52-6b7d-4e0a-9c11-2d5a7b3e9f01", Type: pix.KeyEVP, AccountID: "acc"},
	}}
	client := walletv1.NewKeyServiceClient(dialWith(t, fakeLedger{}, f))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.ListKeys(ctx, &walletv1.ListKeysRequest{AccountId: "acc"})
	if err != nil || len(res.Keys) != 2 || res.Keys[0].Key != "a@example.com" || res.Keys[1].Type != walletv1.KeyType_KEY_TYPE_EVP {
		t.Fatalf("list: %v, %v", res, err)
	}
	if _, err := client.RemoveKey(ctx, &walletv1.RemoveKeyRequest{AccountId: "acc", Key: "a@example.com"}); err != nil || f.removed != [2]string{"acc", "a@example.com"} {
		t.Fatalf("remove: %v, called with %v", err, f.removed)
	}
}

func TestListAndRemoveKeyErrors(t *testing.T) {
	tests := []struct {
		err  error
		want codes.Code
	}{
		{ledger.ErrAccountNotFound, codes.NotFound},
		{pixkey.ErrKeyNotFound, codes.NotFound},
		{pixkey.ErrInvalid, codes.InvalidArgument},
		{&pix.APIError{Status: 503}, codes.Unavailable},
		{errors.New("connection refused to 10.0.0.5"), codes.Internal},
	}
	for _, tt := range tests {
		client := walletv1.NewKeyServiceClient(dialWith(t, fakeLedger{}, &fakeKeys{err: tt.err}))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, errList := client.ListKeys(ctx, &walletv1.ListKeysRequest{AccountId: "acc"})
		_, errRemove := client.RemoveKey(ctx, &walletv1.RemoveKeyRequest{AccountId: "acc", Key: "k"})
		cancel()
		for _, err := range []error{errList, errRemove} {
			if status.Code(err) != tt.want || strings.Contains(err.Error(), "10.0.0.5") {
				t.Errorf("%v: want %v, got %v", tt.err, tt.want, err)
			}
		}
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

func TestKeyServiceIsOptional(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := walletv1.NewKeyServiceClient(dial(t, fakeLedger{})).RegisterKey(ctx, &walletv1.RegisterKeyRequest{}); status.Code(err) != codes.Unimplemented {
		t.Errorf("service not registered: %v", err)
	}
}
