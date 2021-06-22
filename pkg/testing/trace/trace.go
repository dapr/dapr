package trace

import (
	"strconv"

	"go.opencensus.io/trace"

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

// Exporter is an OpenCensus string exporter.
type Exporter struct {
	Buffer *string
	logger logger.Logger
}

// ExportSpan exports span content to the buffer.
func (se *Exporter) ExportSpan(sd *trace.SpanData) {
	*se.Buffer = strconv.Itoa(int(sd.Status.Code))
}

// Register creates a new string exporter endpoint and reporter.
func (se *Exporter) Register(daprID string) {
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	trace.RegisterExporter(se)
}
