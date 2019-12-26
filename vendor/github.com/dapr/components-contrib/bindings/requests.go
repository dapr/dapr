// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package bindings

// WriteRequest is the object given to an dapr output binding
type WriteRequest struct {
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}
