// Package tracing sets up OpenTelemetry tracing. Spans are exported over
// OTLP/HTTP (e.g. to Jaeger) when an OTLP endpoint is configured with the
// standard environment variables (OTEL_EXPORTER_OTLP_ENDPOINT or
// OTEL_EXPORTER_OTLP_TRACES_ENDPOINT, plus OTEL_SERVICE_NAME,
// OTEL_TRACES_SAMPLER, ...); otherwise tracing stays a no-op.
//
// Code that records spans gets its tracer from otel.Tracer, so it works the
// same either way.
package tracing

import (
	"context"
	"fmt"
	"os"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

// DefaultServiceName names this service in traces unless OTEL_SERVICE_NAME is set.
const DefaultServiceName = "customer-data"

// Enabled reports whether an OTLP endpoint is configured.
func Enabled() bool {
	return os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") != "" || os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") != ""
}

// Setup installs the global tracer provider and propagator. The returned
// shutdown flushes buffered spans; call it before exiting. Without an OTLP
// endpoint it does nothing and returns a no-op shutdown.
func Setup(ctx context.Context) (shutdown func(context.Context) error, err error) {
	if !Enabled() {
		return func(context.Context) error { return nil }, nil
	}
	exporter, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, fmt.Errorf("tracing: OTLP exporter: %w", err)
	}
	// Environment attributes (OTEL_SERVICE_NAME, OTEL_RESOURCE_ATTRIBUTES)
	// win over the default service name.
	res, err := resource.Merge(
		resource.NewSchemaless(semconv.ServiceName(DefaultServiceName)),
		resource.Environment(),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing: resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{},
	))
	return provider.Shutdown, nil
}
