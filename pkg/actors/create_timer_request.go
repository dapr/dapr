// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// CreateTimerRequest is the request object to create a new timer.
type CreateTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
	DueTime   string      `json:"dueTime"`
	Period    string      `json:"period"`
	TTL       string      `json:"ttl"`
	Callback  string      `json:"callback"`
	Data      interface{} `json:"data"`
}
