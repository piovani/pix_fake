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
	"google.golang.org/protobuf/types/known/timestamppb"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
)

// Ledger is what the API needs from the ledger module, declared here so the adapter is tested without a database.
type Ledger interface {
	Balance(ctx context.Context, accountID string) (ledger.Balance, error)
}

type accountService struct {
	walletv1.UnimplementedAccountServiceServer
	ledger   Ledger
	payments Payments
}

func (s *accountService) GetStatement(ctx context.Context, req *walletv1.GetStatementRequest) (*walletv1.GetStatementResponse, error) {
	if s.payments == nil {
		return nil, status.Error(codes.Unimplemented, "method GetStatement not implemented")
	}
	st, err := s.payments.Statement(ctx, payment.StatementInput{
		AccountID: req.GetAccountId(), PageSize: int(req.GetPageSize()), PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, paymentError("get statement", err)
	}
	res := &walletv1.GetStatementResponse{NextPageToken: st.NextToken}
	for _, e := range st.Entries {
		kind := walletv1.EntryType_ENTRY_TYPE_DEBIT
		if e.Credit {
			kind = walletv1.EntryType_ENTRY_TYPE_CREDIT
		}
		res.Entries = append(res.Entries, &walletv1.StatementEntry{
			Id: e.ID, Type: kind, AmountCents: e.AmountCents, PaymentId: e.PaymentID, CounterpartyName: e.CounterpartyName,
			CreatedAt: timestamppb.New(e.CreatedAt),
		})
	}
	return res, nil
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

func New(l Ledger, keys Keys, payments Payments) *grpc.Server {
	s := grpc.NewServer()
	walletv1.RegisterAccountServiceServer(s, &accountService{ledger: l, payments: payments})
	if keys != nil {
		walletv1.RegisterKeyServiceServer(s, &keyService{keys: keys})
	}
	if payments != nil {
		walletv1.RegisterPaymentServiceServer(s, &paymentService{payments: payments})
	}

	h := health.NewServer()
	healthpb.RegisterHealthServer(s, h)
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	return s
}
