// Package grpcapi is the wallet's gRPC adapter: it turns calls into use of the modules and holds no business rule.
package grpcapi

import (
	"context"
	"errors"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
)

// Ledger is what the API needs from the ledger module, declared here so the adapter is tested without a database.
type Ledger interface {
	Balance(ctx context.Context, accountID string) (ledger.Balance, error)
}

// accountService answers wallet.v1.AccountService. GetStatement returns Unimplemented for now.
type accountService struct {
	walletv1.UnimplementedAccountServiceServer
	ledger Ledger
}

func (s *accountService) GetBalance(ctx context.Context, req *walletv1.GetBalanceRequest) (*walletv1.GetBalanceResponse, error) {
	if req.GetAccountId() == "" {
		return nil, status.Error(codes.InvalidArgument, "account_id is required")
	}
	b, err := s.ledger.Balance(ctx, req.GetAccountId())
	switch {
	case errors.Is(err, ledger.ErrAccountNotFound):
		return nil, status.Error(codes.NotFound, "account not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "could not read the balance")
	}
	return &walletv1.GetBalanceResponse{AvailableCents: b.AvailableCents, ReservedCents: b.ReservedCents}, nil
}

// New builds the gRPC server with the account service, the key service (when keys is not nil) and the health service.
func New(l Ledger, keys Keys) *grpc.Server {
	s := grpc.NewServer()
	walletv1.RegisterAccountServiceServer(s, &accountService{ledger: l})
	if keys != nil {
		walletv1.RegisterKeyServiceServer(s, &keyService{keys: keys})
	}

	h := health.NewServer()
	healthpb.RegisterHealthServer(s, h)
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	return s
}
