package exporters

import (
	"strconv"

	"go.opencensus.io/trace"
)

// StringExporter is an OpenCensus string exporter
type StringExporter struct {
	Buffer *string
}

func (se StringExporter) ExportSpan(sd *trace.SpanData) {
	*se.Buffer = strconv.Itoa(int(sd.Status.Code))
}

// Init creates a new zipkin endpoint and reporter
func (z StringExporter) Init(actionsID string, actionsAddress string, exporterAddress string) error {
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	trace.RegisterExporter(z)
	return nil
}
