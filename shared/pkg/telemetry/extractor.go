package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// ExtractTraceparent mengekstrak traceparent dari context untuk disimpan ke database Outbox
func ExtractTraceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier.Get("traceparent")
}

// InjectTraceparent memasukkan traceparent dari Kafka Header ke context baru
func InjectTraceparent(ctx context.Context, traceparent string) context.Context {
	if traceparent == "" {
		return ctx
	}
	carrier := propagation.MapCarrier{
		"traceparent": traceparent,
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}
