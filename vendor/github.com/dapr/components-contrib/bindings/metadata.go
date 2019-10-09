// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

// Metadata represents a set of binding specific properties
type Metadata struct {
	Name       string
	Properties map[string]string `json:"properties"`
}
