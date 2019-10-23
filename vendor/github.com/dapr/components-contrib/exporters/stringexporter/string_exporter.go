// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package stringexporter

import (
	"strconv"

	"github.com/dapr/components-contrib/exporters"
	"go.opencensus.io/trace"
)

// Metadata is the zipkin config
type stringExporterMetadata struct {
	Buffer *string
}

// NewStringExporter returns a new string exporter instance
func NewStringExporter() *Exporter {
	return &Exporter{}
}

// Exporter is an OpenCensus string exporter
type Exporter struct {
	Buffer *string
}

// ExportSpan exports span content to the buffer
func (se *Exporter) ExportSpan(sd *trace.SpanData) {
	*se.Buffer = strconv.Itoa(int(sd.Status.Code))
}

// Init creates a new zipkin endpoint and reporter
func (se *Exporter) Init(daprID string, hostAddress string, metadata exporters.Metadata) error {
	se.Buffer = metadata.Buffer
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	trace.RegisterExporter(se)
	return nil
}
