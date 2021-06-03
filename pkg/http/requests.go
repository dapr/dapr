// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package http

// OutputBindingRequest is the request object to invoke an output binding.
type OutputBindingRequest struct {
	Metadata  map[string]string `json:"metadata"`
	Data      interface{}       `json:"data"`
	Operation string            `json:"operation"`
}

// BulkGetRequest is the request object to get a list of values for multiple keys from a state store.
type BulkGetRequest struct {
	Metadata    map[string]string `json:"metadata"`
	Keys        []string          `json:"keys"`
	Parallelism int               `json:"parallelism"`
}
