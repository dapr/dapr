package exporters

import (
	"strconv"

	"go.opencensus.io/trace"
)

// StringExporter is an OpenCensus string exporter
type StringExporter struct {
	Buffer *string
}

// ExportSpan exports span content to the buffer
func (se StringExporter) ExportSpan(sd *trace.SpanData) {
	*se.Buffer = strconv.Itoa(int(sd.Status.Code))
}

// Init creates a new zipkin endpoint and reporter
func (se StringExporter) Init(daprID string, daprAddress string, exporterAddress string) error {
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	trace.RegisterExporter(se)
	return nil
}
