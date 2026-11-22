// Command api runs the wallet's gRPC API. The worker (cmd/worker) is a separate process.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"pixfake/wallet/internal/grpcapi"
	"pixfake/wallet/internal/ledger"
	"pixfake/wallet/internal/payment"
	"pixfake/wallet/internal/pix"
	"pixfake/wallet/internal/pixkey"
	"pixfake/wallet/internal/platform/config"
	"pixfake/wallet/internal/platform/db"
	"pixfake/wallet/internal/platform/telemetry"
)

func main() {
	cfg := config.Load()
	log, shutdown, err := telemetry.Setup(context.Background(), "wallet-api", cfg.OTLP)
	if err != nil {
		slog.Error("telemetry", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(log)
	defer flush(shutdown)

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		slog.Error("migrate", "err", err)
		os.Exit(1)
	}

	pool, err := db.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		slog.Error("connect db", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	lis, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		slog.Error("listen", "addr", cfg.GRPCAddr, "err", err)
		os.Exit(1)
	}
	accounts := ledger.New(pool)
	dict := pix.NewDictClient(cfg.DictBaseURL, cfg.ISPB)
	keys := pixkey.New(accounts, dict, pixkey.NewPostgres(pool), cfg.ISPB)
	payments := payment.New(accounts, keys, dict, pix.NewSPIClient(cfg.SPIBaseURL, cfg.ISPB), payment.NewPostgres(pool), cfg.ISPB)
	srv := grpcapi.New(accounts, keys, payments, grpc.StatsHandler(otelgrpc.NewServerHandler()))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		slog.Info("shutting down")
		srv.GracefulStop()
	}()

	slog.Info("listening", "addr", cfg.GRPCAddr)
	if err := srv.Serve(lis); err != nil {
		slog.Error("serve", "err", err)
	}
}

func flush(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := shutdown(ctx); err != nil {
		slog.Warn("telemetry flush", "err", err)
	}
}
