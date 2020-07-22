// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// Reminder represents a persisted reminder for a unique actor
type Reminder struct {
	ReminderID     int         `json:"reminderID"`
	ActorID        string      `json:"actorID,omitempty"`
	ActorType      string      `json:"actorType,omitempty"`
	Name           string      `json:"name,omitempty"`
	Data           interface{} `json:"data"`
	Period         string      `json:"period"`
	DueTime        string      `json:"dueTime"`
	RegisteredTime string      `json:"registeredTime,omitempty"`
}

// ActiveReminder info cached in memory
type ActiveReminder struct {
	ID       int       `json:"reminderID"`
	StopChan chan bool `json:"stopChannel"`
}
