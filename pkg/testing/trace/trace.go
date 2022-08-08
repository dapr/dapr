package trace

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"

	"github.com/dapr/kit/logger"
)

// NewStringExporter returns a new string exporter instance.
//
// It is very useful in testing scenario where we want to validate trace propagation.
func NewStringExporter(buffer *string, logger logger.Logger) *Exporter {
	return &Exporter{
		Buffer: buffer,
		logger: logger,
	}
}

// Exporter is an OpenTelemetry string exporter.
type Exporter struct {
	Buffer *string
	logger logger.Logger
}

// ExportSpan exports span content to the buffer.
func (se *Exporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	*se.Buffer = spans[0].Status().Code.String()
	return nil
}

// ExportSpan exports span content to the buffer.
func (se *Exporter) Shutdown(ctx context.Context) error {
	return nil
}

// Register creates a new string exporter endpoint and reporter.
func (se *Exporter) Register(daprID string) {
	// Register a resource
	r := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceNameKey.String(daprID),
	)
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(se),
		sdktrace.WithResource(r),
	)
	otel.SetTracerProvider(tp)
}
