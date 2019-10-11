// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// StateResponse is the response returned from getting an actor state
type StateResponse struct {
	Data []byte `json:"data"`
}
