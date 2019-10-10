// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

// Metadata contains a state store specific set of metadata properties
type Metadata struct {
	Properties map[string]string `json:"properties"`
}
