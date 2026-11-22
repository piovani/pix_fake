package pix

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	meter        = otel.Meter("pixfake/mock/spi/pix")
	finalized, _ = meter.Int64Counter("spi.payments.finalized", metric.WithDescription("payments and returns that settled or were rejected"))
	refused, _   = meter.Int64Counter("spi.orders.refused", metric.WithDescription("orders the SPI itself rejected, before any receiver saw them"))
)

func countFinalized(ctx context.Context, pay Payment, status, reason string) {
	finalized.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", pay.Kind), attribute.String("status", status), attribute.String("reason", reason)))
}

func countRefused(ctx context.Context, kind, reason string) {
	refused.Add(ctx, 1, metric.WithAttributes(attribute.String("kind", kind), attribute.String("reason", reason)))
}

// tag marks the request's span with the payment it is about, so a search finds every service that touched it.
func tag(ctx context.Context, endToEndID, returnID string) {
	kv := []attribute.KeyValue{attribute.String("pix.end_to_end_id", endToEndID)}
	if returnID != "" {
		kv = append(kv, attribute.String("pix.return_id", returnID))
	}
	trace.SpanFromContext(ctx).SetAttributes(kv...)
}
