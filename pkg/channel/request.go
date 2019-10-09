// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package channel

// InvokeRequest is the request object for invoking a user code method
type InvokeRequest struct {
	Method   string            `json:"method"`
	Payload  []byte            `json:"payload"`
	Metadata map[string]string `json:"metadata"`
}
