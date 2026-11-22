package payment

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

var (
	tracer      trace.Tracer = otel.Tracer("pixfake/wallet/payment")
	meter                    = otel.Meter("pixfake/wallet/payment")
	finished, _              = meter.Int64Counter("wallet.payments.finished", metric.WithDescription("payments that reached a final status"))
	amount, _                = meter.Int64Counter("wallet.payments.amount", metric.WithUnit("{centavo}"), metric.WithDescription("money of the settled payments"))
)

// close records a final status and counts it once, when it actually changed.
func (s *Service) close(ctx context.Context, p Payment, status Status, code, reason string, at time.Time) error {
	changed, err := s.store.Close(ctx, p.ID, status, code, reason, at)
	if err != nil || !changed {
		return err
	}
	attrs := metric.WithAttributes(
		attribute.String("direction", string(p.Direction)),
		attribute.String("type", string(p.Type)),
		attribute.String("status", string(status)),
		attribute.String("reason", code),
	)
	finished.Add(ctx, 1, attrs)
	if status == Settled {
		amount.Add(ctx, p.AmountCents, attrs)
	}
	return nil
}
