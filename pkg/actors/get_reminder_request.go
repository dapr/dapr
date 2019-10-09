// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// GetReminderRequest is the request object to get an existing reminder
type GetReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}
