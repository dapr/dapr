// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package native

import (
	"encoding/json"
	"strconv"

	"contrib.go.opencensus.io/exporter/ocagent"
	"github.com/dapr/components-contrib/exporters"
	"go.opencensus.io/trace"
)

// Metadata is the native exporter config
type nativeExporterMetadata struct {
	AgentEndpoint string `json:"agentEndpoint"`
	Enabled       string `json:"enabled"`
}

// NewNativeExporter returns a new native exporter instance
func NewNativeExporter() *Exporter {
	return &Exporter{}
}

// Exporter is an OpenCensus native exporter
type Exporter struct {
}

// Init creates a new native endpoint and reporter
func (l *Exporter) Init(daprID string, hostAddress string, metadata exporters.Metadata) error {
	meta, err := l.getNativeMetadata(metadata)
	if err != nil {
		return err
	}

	enabled, _ := strconv.ParseBool(meta.Enabled)
	if !enabled {
		return nil
	}

	exporter, err := ocagent.NewExporter(ocagent.WithInsecure(), ocagent.WithServiceName(daprID), ocagent.WithAddress(meta.AgentEndpoint))
	if err != nil {
		return err
	}

	trace.RegisterExporter(exporter)
	trace.ApplyConfig(trace.Config{DefaultSampler: trace.AlwaysSample()})
	return nil
}

func (l *Exporter) getNativeMetadata(metadata exporters.Metadata) (*nativeExporterMetadata, error) {
	b, err := json.Marshal(metadata.Properties)
	if err != nil {
		return nil, err
	}

	var nativeExporterMetadata nativeExporterMetadata
	err = json.Unmarshal(b, &nativeExporterMetadata)
	if err != nil {
		return nil, err
	}
	return &nativeExporterMetadata, nil
}
