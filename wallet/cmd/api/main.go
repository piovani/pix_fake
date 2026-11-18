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
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
)

func main() {
	cfg := config.Load()

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}
	
	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect to the database", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		slog.Error("listen", "addr", cfg.GRPCAddr, "err", err)
		os.Exit(1)
	}
	srv := grpcapi.New(ledger.New(pool))

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
