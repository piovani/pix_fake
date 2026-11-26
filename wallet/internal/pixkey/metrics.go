package pixkey

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

var missingInDict, _ = otel.Meter("pixfake/wallet/pixkey").Int64Gauge("wallet.keys.missing_in_dict",
	metric.WithDescription("keys of the wallet's copy that the DICT did not have at the last check"))
