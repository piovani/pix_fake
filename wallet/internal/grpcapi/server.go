// Package grpcapi is the wallet's gRPC adapter: it turns calls into use of the modules (ledger, payment, pix) and
// holds no business rule. Every service of api/proto is registered here.
package grpcapi

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	walletv1 "pixfake/wallet/gen/wallet/v1"
)

// walletService answers wallet.v1.WalletService. Calls that the modules do not support yet return Unimplemented.
type walletService struct {
	walletv1.UnimplementedWalletServiceServer
}

// New builds the gRPC server with the wallet service and the standard health service.
func New() *grpc.Server {
	s := grpc.NewServer()
	walletv1.RegisterWalletServiceServer(s, &walletService{})

	h := health.NewServer()
	healthpb.RegisterHealthServer(s, h)
	h.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	return s
}
