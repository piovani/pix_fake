// Command simulator is a web page that drives every situation of the Pix environment (payments from the wallet or from a
// simulated bank, returns, keys in the wallet and in the DICT, account status, faults) and links each action to its
// traces and logs in Grafana. It talks to the wallet over gRPC and to the mocks over their HTTP APIs, like any client.
package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"pixfake/simulator/internal/telemetry"
)

//go:embed web
var web embed.FS

type config struct {
	Addr, Wallet, DICT, SPI, Grafana, WalletDir, OTLP string
}

func load() config {
	return config{
		Addr:      env("SIM_ADDR", ":8090"),
		Wallet:    env("SIM_WALLET_ADDR", "localhost:9090"),
		DICT:      env("SIM_DICT_URL", "http://localhost:8080"),
		SPI:       env("SIM_SPI_URL", "http://localhost:8081"),
		Grafana:   env("SIM_GRAFANA_URL", "http://localhost:3000"),
		WalletDir: env("SIM_WALLET_DIR", "../wallet"),
		OTLP:      env("OTEL_EXPORTER_OTLP_ENDPOINT", "http://localhost:4318"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := load()
	log, shutdown, err := telemetry.Setup(context.Background(), "simulator", cfg.OTLP)
	if err != nil {
		slog.Error("telemetry", "err", err)
		os.Exit(1)
	}
	slog.SetDefault(log)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdown(ctx)
	}()

	conn, err := grpc.NewClient(cfg.Wallet, grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()))
	if err != nil {
		slog.Error("wallet client", "err", err)
		os.Exit(1)
	}
	defer conn.Close()

	sim := newSim(cfg, conn)
	static, _ := fs.Sub(web, "web")
	mux := http.NewServeMux()
	sim.routes(mux)
	mux.Handle("GET /", http.FileServerFS(static))

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: otelhttp.NewHandler(withTraceID(mux), "simulator",
			otelhttp.WithFilter(func(r *http.Request) bool { return isAction(r) }),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return "sim " + r.Method + " " + r.URL.Path })),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	slog.Info("simulator listening", "url", "http://localhost"+cfg.Addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("serve", "err", err)
	}
}

// Only actions get a trace; the page polls a lot, and tracing every read would bury what the user did.
func isAction(r *http.Request) bool {
	return r.Method != http.MethodGet
}
