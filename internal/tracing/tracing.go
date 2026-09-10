// Package tracing sets up OpenTelemetry distributed tracing for HTTP
// requests. It's optional: when disabled, the global tracer provider is
// left as OpenTelemetry's built-in no-op, so callers can use Tracer()
// unconditionally without checking whether tracing is on.
package tracing

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/milosursulovic/vortex/internal/config"
)

const tracerName = "github.com/milosursulovic/vortex"

// Shutdown flushes buffered spans and stops the tracer provider.
type Shutdown func(context.Context) error

// Tracer returns VORTEX's tracer. Safe to call before Setup: until Setup
// installs a real provider, spans are cheap no-ops.
func Tracer() trace.Tracer {
	return otel.Tracer(tracerName)
}

// Setup installs the global tracer provider and propagator per cfg. If
// cfg.Enabled is false it does nothing (the no-op default stays active)
// and returns a no-op Shutdown.
func Setup(cfg config.TracingConfig, logger *slog.Logger) (Shutdown, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := newExporter(cfg)
	if err != nil {
		return nil, fmt.Errorf("tracing exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(semconv.ServiceName(cfg.ServiceName)),
	)
	if err != nil {
		return nil, fmt.Errorf("tracing resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(cfg.SampleRatio)),
	)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	logger.Info("tracing_enabled", "exporter", cfg.Exporter, "service_name", cfg.ServiceName, "sample_ratio", cfg.SampleRatio)

	return tp.Shutdown, nil
}

func newExporter(cfg config.TracingConfig) (sdktrace.SpanExporter, error) {
	switch cfg.Exporter {
	case "otlp-http":
		return otlptracehttp.New(context.Background(),
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithInsecure(),
		)
	default: // "stdout"
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	}
}
