// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// CreateReminderRequest is the request object to create a new reminder.
type CreateReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
	Data      interface{} `json:"data"`
	DueTime   string      `json:"dueTime"`
	Period    string      `json:"period"`
	TTL       string      `json:"ttl"`
}
