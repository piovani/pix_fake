package grpcapi

import (
	"context"
	"errors"
	"log/slog"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
)

// Keys is what the API needs from the pixkey module.
type Keys interface {
	Register(ctx context.Context, in pixkey.RegisterInput) (pixkey.Key, error)
	List(ctx context.Context, accountID string) ([]pixkey.Key, error)
	Remove(ctx context.Context, accountID, key string) error
}

// keyService answers wallet.v1.KeyService.
type keyService struct {
	walletv1.UnimplementedKeyServiceServer
	keys Keys
}

var keyTypes = map[walletv1.KeyType]pix.KeyType{
	walletv1.KeyType_KEY_TYPE_CPF:   pix.KeyCPF,
	walletv1.KeyType_KEY_TYPE_CNPJ:  pix.KeyCNPJ,
	walletv1.KeyType_KEY_TYPE_PHONE: pix.KeyPhone,
	walletv1.KeyType_KEY_TYPE_EMAIL: pix.KeyEmail,
	walletv1.KeyType_KEY_TYPE_EVP:   pix.KeyEVP,
}

func (s *keyService) RegisterKey(ctx context.Context, req *walletv1.RegisterKeyRequest) (*walletv1.RegisterKeyResponse, error) {
	k, err := s.keys.Register(ctx, pixkey.RegisterInput{
		AccountID: req.GetAccountId(),
		Type:      keyTypes[req.GetType()],
		Key:       req.GetKey(),
		RequestID: req.GetRequestId(),
	})
	if err != nil {
		return nil, keyError("register key", err)
	}
	return &walletv1.RegisterKeyResponse{Key: toProtoKey(k)}, nil
}

func (s *keyService) ListKeys(ctx context.Context, req *walletv1.ListKeysRequest) (*walletv1.ListKeysResponse, error) {
	keys, err := s.keys.List(ctx, req.GetAccountId())
	if err != nil {
		return nil, keyError("list keys", err)
	}
	res := &walletv1.ListKeysResponse{}
	for _, k := range keys {
		res.Keys = append(res.Keys, toProtoKey(k))
	}
	return res, nil
}

func (s *keyService) RemoveKey(ctx context.Context, req *walletv1.RemoveKeyRequest) (*walletv1.RemoveKeyResponse, error) {
	if err := s.keys.Remove(ctx, req.GetAccountId(), req.GetKey()); err != nil {
		return nil, keyError("remove key", err)
	}
	return &walletv1.RemoveKeyResponse{}, nil
}

func keyError(op string, err error) error {
	switch {
	case errors.Is(err, pixkey.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ledger.ErrAccountNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, pixkey.ErrKeyNotFound):
		return status.Error(codes.NotFound, "key not found for this account")
	case errors.Is(err, pixkey.ErrKeyTaken):
		return status.Error(codes.AlreadyExists, "key already registered")
	case errors.Is(err, pixkey.ErrRequestConflict):
		return status.Error(codes.AlreadyExists, "request_id reused with other data")
	case errors.Is(err, pix.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, "dict rate limit, try again later")
	case errors.Is(err, pix.ErrUnavailable):
		return status.Error(codes.Unavailable, "dict unavailable, try again later")
	}
	slog.Error(op, "err", err)
	return status.Error(codes.Internal, "internal error")
}

func toProtoKey(k pixkey.Key) *walletv1.Key {
	out := &walletv1.Key{Key: k.Key, AccountId: k.AccountID, CreatedAt: timestamppb.New(k.CreatedAt)}
	for p, d := range keyTypes {
		if d == k.Type {
			out.Type = p
		}
	}
	return out
}
