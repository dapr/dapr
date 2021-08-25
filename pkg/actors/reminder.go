// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// Reminder represents a persisted reminder for a unique actor.
type Reminder struct {
	ActorID        string      `json:"actorID,omitempty"`
	ActorType      string      `json:"actorType,omitempty"`
	Name           string      `json:"name,omitempty"`
	Data           interface{} `json:"data"`
	Period         string      `json:"period"`
	DueTime        string      `json:"dueTime"`
	RegisteredTime string      `json:"registeredTime,omitempty"`
	ExpirationTime string      `json:"expirationTime,omitempty"`
}
