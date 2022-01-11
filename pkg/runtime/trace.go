/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package runtime

import (
	"go.opencensus.io/trace"
)

// traceExporterStore allows us to capture the trace exporter store registrations.
//
// This is needed because the OpenCensus library only expose global methods for
// exporter registration.
type traceExporterStore interface {
	// RegisterExporter registers a trace.Exporter.
	RegisterExporter(exporter trace.Exporter)
}

// openCensusExporterStore is an implementation of traceExporterStore
// that makes use of OpenCensus's library's global exporer stores (`trace`).
type openCensusExporterStore struct{}

// RegisterExporter implements traceExporterStore using OpenCensus's global registration.
func (s openCensusExporterStore) RegisterExporter(exporter trace.Exporter) {
	trace.RegisterExporter(exporter)
}

// fakeTraceExporterStore implements traceExporterStore by merely record the exporters
// and config that were registered/applied.
//
// This is only for use in unit tests.
type fakeTraceExporterStore struct {
	exporters []trace.Exporter
}

// RegisterExporter records the given trace.Exporter.
func (r *fakeTraceExporterStore) RegisterExporter(exporter trace.Exporter) {
	r.exporters = append(r.exporters, exporter)
}
