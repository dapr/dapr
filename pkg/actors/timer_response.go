// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package actors

// TimerResponse is the response object send to an Actor SDK API when a timer fires.
type TimerResponse struct {
	Callback string      `json:"callback"`
	Data     interface{} `json:"data"`
	DueTime  string      `json:"dueTime"`
	Period   string      `json:"period"`
}
