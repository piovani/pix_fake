// Command api runs the wallet's gRPC API. The worker (cmd/worker) is a separate process.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"

	"pixfake/wallet/internal/grpcapi"
	"pixfake/wallet/internal/platform/config"
)

func main() {
	cfg := config.Load()

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		slog.Error("listen", "addr", cfg.GRPCAddr, "err", err)
		os.Exit(1)
	}
	srv := grpcapi.New()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		srv.GracefulStop()
	}()

	slog.Info("gRPC API listening", "addr", cfg.GRPCAddr)
	if err := srv.Serve(lis); err != nil {
		slog.Error("serve", "err", err)
		os.Exit(1)
	}
}
