package trace

import (
	"context"
	"slices"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	"go.opentelemetry.io/otel/trace"

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

// StartedSpan is a snapshot, taken when a span is started, of the properties a
// test needs in order to tell a new root span from a continued trace.
type StartedSpan struct {
	Name string
	// SpanContext is the started span's own context.
	SpanContext trace.SpanContext
	// Parent is the span context the span was started from. An invalid Parent
	// means the span is a new root.
	Parent trace.SpanContext
}

// SpanRecorder records every span that is started, whether or not that span is
// ever ended. A span that is started and never ended never reaches an exporter,
// so OnStart is the only place a leaked span can be observed.
type SpanRecorder struct {
	lock    sync.Mutex
	started []StartedSpan
	ended   map[trace.SpanID]struct{}
}

// NewSpanRecorder installs a recorder as the global tracer provider's span
// processor and returns it.
//
// The global provider accepts a delegate only once per process, so a test
// binary gets one recorder: call this from a single parent test or TestMain and
// share it, rather than once per test. Because the recorder is process-wide,
// tests that run in parallel with span-producing tests should select the spans
// they care about by span ID rather than assert on the full set.
func NewSpanRecorder() *SpanRecorder {
	r := &SpanRecorder{ended: make(map[trace.SpanID]struct{})}
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(r)))

	return r
}

// OnStart implements sdktrace.SpanProcessor, recording the span as it starts.
func (s *SpanRecorder) OnStart(_ context.Context, span sdktrace.ReadWriteSpan) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.started = append(s.started, StartedSpan{
		Name:        span.Name(),
		SpanContext: span.SpanContext(),
		Parent:      span.Parent(),
	})
}

// OnEnd implements sdktrace.SpanProcessor, marking the span as ended.
func (s *SpanRecorder) OnEnd(span sdktrace.ReadOnlySpan) {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.ended[span.SpanContext().SpanID()] = struct{}{}
}

// Shutdown and ForceFlush complete sdktrace.SpanProcessor. The recorder keeps
// everything it sees in memory and has nothing to flush.
func (s *SpanRecorder) Shutdown(context.Context) error   { return nil }
func (s *SpanRecorder) ForceFlush(context.Context) error { return nil }

// Reset drops everything recorded so far.
func (s *SpanRecorder) Reset() {
	s.lock.Lock()
	defer s.lock.Unlock()

	s.started = nil
	clear(s.ended)
}

// Ended reports whether the span with the given span ID has been ended. A
// started span that is never ended never reaches an exporter, so this is how a
// test catches a delivery path that leaks one.
func (s *SpanRecorder) Ended(id trace.SpanID) bool {
	s.lock.Lock()
	defer s.lock.Unlock()

	_, ok := s.ended[id]

	return ok
}

// Started returns every span started since the last Reset.
func (s *SpanRecorder) Started() []StartedSpan {
	s.lock.Lock()
	defer s.lock.Unlock()

	return slices.Clone(s.started)
}

// Names returns the name of every span started since the last Reset.
func (s *SpanRecorder) Names() []string {
	started := s.Started()

	names := make([]string, len(started))
	for i, span := range started {
		names[i] = span.Name
	}

	return names
}

// BySpanID returns the recorded span with the given span ID. Tests running
// alongside other span-producing tests use it to pick out their own span.
func (s *SpanRecorder) BySpanID(id trace.SpanID) (StartedSpan, bool) {
	for _, span := range s.Started() {
		if span.SpanContext.SpanID() == id {
			return span, true
		}
	}

	return StartedSpan{}, false
}
