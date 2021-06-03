// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// SaveStateRequest is the request object for saving an actor state.
type SaveStateRequest struct {
	ActorID   string      `json:"actorId"`
	ActorType string      `json:"actorType"`
	Key       string      `json:"key"`
	Value     interface{} `json:"value"`
}
