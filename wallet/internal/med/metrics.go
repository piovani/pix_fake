package med

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter       = otel.Meter("pixfake/wallet/med")
	blocked, _  = meter.Int64Counter("wallet.med.blocked", metric.WithUnit("{cent}"), metric.WithDescription("money blocked for infraction reports"))
	refunds, _  = meter.Int64Counter("wallet.med.refunds", metric.WithDescription("refunds of the MED the wallet answered, by result"))
	contests, _ = meter.Int64Counter("wallet.med.contests", metric.WithDescription("payments the wallet's customers contested"))
)

func resultAttr(result string) metric.MeasurementOption {
	return metric.WithAttributes(attribute.String("result", result))
}
