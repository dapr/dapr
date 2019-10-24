// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

// Exporter is the interface for tracing exporter wrappers
type Exporter interface {
	Init(daprID string, hostAddress string, metadata Metadata) error
}
