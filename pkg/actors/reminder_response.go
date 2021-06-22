// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// ReminderResponse is the payload that is sent to an Actor SDK API for execution.
type ReminderResponse struct {
	Data    interface{} `json:"data"`
	DueTime string      `json:"dueTime"`
	Period  string      `json:"period"`
}
