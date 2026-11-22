// Package telemetry sends traces, metrics and logs over OTLP/HTTP. With no endpoint, or OTEL_SDK_DISABLED=true, nothing is
// exported and logs only go to stderr.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// Setup returns the logger to use and a shutdown that flushes what is buffered. endpoint is the collector's base URL.
func Setup(ctx context.Context, service, endpoint string) (*slog.Logger, func(context.Context) error, error) {
	stderr := slog.NewTextHandler(os.Stderr, nil)
	if endpoint == "" || os.Getenv("OTEL_SDK_DISABLED") == "true" {
		return slog.New(stderr), func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithAttributes(attribute.String("service.name", service)))
	if err != nil {
		return nil, nil, err
	}
	traces, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(endpoint+"/v1/traces"))
	if err != nil {
		return nil, nil, err
	}
	metrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(endpoint+"/v1/metrics"))
	if err != nil {
		return nil, nil, err
	}
	logs, err := otlploghttp.New(ctx, otlploghttp.WithEndpointURL(endpoint+"/v1/logs"))
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(traces), sdktrace.WithResource(res))
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics, sdkmetric.WithInterval(10*time.Second))),
		sdkmetric.WithResource(res))
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewBatchProcessor(logs)), sdklog.WithResource(res))

	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetLoggerProvider(lp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	otel.SetErrorHandler(quietErrors(stderr))
	if err := runtime.Start(); err != nil {
		return nil, nil, err
	}

	log := slog.New(slog.NewMultiHandler(stderr, otelslog.NewHandler(service, otelslog.WithLoggerProvider(lp))))
	shutdown := func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx), lp.Shutdown(ctx))
	}
	return log, shutdown, nil
}

// quietErrors logs an export failure (the collector is down, say) at most once a minute instead of on every batch.
func quietErrors(h slog.Handler) otel.ErrorHandlerFunc {
	var mu sync.Mutex
	var last time.Time
	log := slog.New(h)
	return func(err error) {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(last) < time.Minute {
			return
		}
		last = time.Now()
		log.Warn("telemetry export failed", "err", err)
	}
}
