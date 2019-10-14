// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package state

// GetResponse is the request object for getting state
type GetResponse struct {
	Data     []byte            `json:"data"`
	ETag     string            `json:"etag,omitempty"`
	Metadata map[string]string `json:"metadata"`
}
