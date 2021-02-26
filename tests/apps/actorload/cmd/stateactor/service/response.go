// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package service

import (
	"encoding/json"
	"io"
)

// ActorResponse is the default response body contract.
type ActorResponse struct {
	Message string `json:"message"`
}

// NewActorResponse creates ActorResponse with the given message.
func NewActorResponse(msg string) ActorResponse {
	return ActorResponse{Message: msg}
}

// Encode serializes ActorResponse to write buffer.
func (e ActorResponse) Encode(w io.Writer) {
	json.NewEncoder(w).Encode(e)
}
