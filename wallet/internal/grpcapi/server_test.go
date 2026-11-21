package grpcapi

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	walletv1 "pixfake/wallet/gen/wallet/v1"
	"pixfake/wallet/internal/ledger"
)

type fakeLedger struct {
	balances map[string]ledger.Balance
	err      error
}

func (f fakeLedger) Balance(_ context.Context, id string) (ledger.Balance, error) {
	if f.err != nil {
		return ledger.Balance{}, f.err
	}
	b, ok := f.balances[id]
	if !ok {
		return ledger.Balance{}, ledger.ErrAccountNotFound
	}
	return b, nil
}

func dial(t *testing.T, l Ledger) *grpc.ClientConn { return dialWith(t, l, nil) }

func dialWith(t *testing.T, l Ledger, keys Keys) *grpc.ClientConn { return dialAll(t, l, keys, nil) }

func dialAll(t *testing.T, l Ledger, keys Keys, payments Payments) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := New(l, keys, payments)
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
	res, err := healthpb.NewHealthClient(dial(t, fakeLedger{})).Check(ctx, &healthpb.HealthCheckRequest{})
	if err != nil || res.Status != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("health: %v, %v", res, err)
	}
}

func TestGetBalance(t *testing.T) {
	l := fakeLedger{balances: map[string]ledger.Balance{"acc": {AvailableCents: 6000, ReservedCents: 4000}}}
	client := walletv1.NewAccountServiceClient(dial(t, l))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := client.GetBalance(ctx, &walletv1.GetBalanceRequest{AccountId: "acc"})
	if err != nil || res.AvailableCents != 6000 || res.ReservedCents != 4000 {
		t.Fatalf("balance: %v, %v", res, err)
	}
	if _, err := client.GetBalance(ctx, &walletv1.GetBalanceRequest{AccountId: "other"}); status.Code(err) != codes.NotFound {
		t.Fatalf("unknown account: want NotFound, got %v", err)
	}
	if _, err := client.GetBalance(ctx, &walletv1.GetBalanceRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("no account: want InvalidArgument, got %v", err)
	}
}

func TestGetBalanceHidesInternalErrors(t *testing.T) {
	client := walletv1.NewAccountServiceClient(dial(t, fakeLedger{err: errors.New("connection refused to 10.0.0.5")}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.GetBalance(ctx, &walletv1.GetBalanceRequest{AccountId: "acc"})
	if status.Code(err) != codes.Internal || strings.Contains(err.Error(), "10.0.0.5") {
		t.Fatalf("want Internal without the cause, got %v", err)
	}
}
