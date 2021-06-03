// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// ActorHostedRequest is the request object for checking if an actor is hosted on this instance.
type ActorHostedRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
}
