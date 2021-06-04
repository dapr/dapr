// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// ReminderTrack is a persisted object that keeps track of the last time a reminder fired.
type ReminderTrack struct {
	LastFiredTime  string `json:"lastFiredTime"`
	RepetitionLeft int    `json:"repetitionLeft"`
}
