// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// RenameReminderRequest is the request object for rename a reminder.
type RenameReminderRequest struct {
	OldName   string
	ActorType string
	ActorID   string
	NewName   string
}
