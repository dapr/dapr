// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// GetReminderRequest is the request object to get an existing reminder.
type GetReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}
