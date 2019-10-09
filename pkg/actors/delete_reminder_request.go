// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// DeleteReminderRequest is the request object for deleting a reminder
type DeleteReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}
