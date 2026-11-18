package grpcapi

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	walletv1 "pixfake/wallet/gen/wallet/v1"
)

func dial(t *testing.T) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := New()
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)

	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestHealthIsServing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := healthpb.NewHealthClient(dial(t)).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil || res.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v, %v", res, err)
	}
}

func TestGetBalanceIsNotImplementedYet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := walletv1.NewWalletServiceClient(dial(t)).GetBalance(ctx, &walletv1.GetBalanceRequest{AccountId: "x"})
	if status.Code(err) != codes.Unimplemented {
		t.Fatalf("want Unimplemented, got %v", err)
	}
}
