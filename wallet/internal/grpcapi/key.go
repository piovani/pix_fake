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
}

// keyService answers wallet.v1.KeyService. ListKeys and RemoveKey return Unimplemented for now.
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
		return nil, registerError(err)
	}
	return &walletv1.RegisterKeyResponse{Key: toProtoKey(k)}, nil
}

func registerError(err error) error {
	switch {
	case errors.Is(err, pixkey.ErrInvalid):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, ledger.ErrAccountNotFound):
		return status.Error(codes.NotFound, "account not found")
	case errors.Is(err, pixkey.ErrKeyTaken):
		return status.Error(codes.AlreadyExists, "the key is already registered")
	case errors.Is(err, pixkey.ErrRequestConflict):
		return status.Error(codes.AlreadyExists, "request_id was already used with other data")
	case errors.Is(err, pix.ErrRateLimited):
		return status.Error(codes.ResourceExhausted, "the DICT is limiting requests, try again later")
	case errors.Is(err, pix.ErrUnavailable):
		return status.Error(codes.Unavailable, "the DICT is unavailable, try again later")
	}
	slog.Error("register key", "err", err)
	return status.Error(codes.Internal, "could not register the key")
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
