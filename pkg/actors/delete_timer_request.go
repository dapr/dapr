// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// DeleteTimerRequest is a request object for deleting a timer.
type DeleteTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
}
