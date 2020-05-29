// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

// OutputBindingRequest is the request object to invoke an output binding
type OutputBindingRequest struct {
	Metadata  map[string]string `json:"metadata"`
	Data      interface{}       `json:"data"`
	Operation string            `json:"operation"`
}
