// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package exporters

// Metadata represents a set of exporter specific properties
type Metadata struct {
	Properties map[string]string `json:"properties"`
	Buffer     *string           `json:"-"`
}
