/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package actors

import (
	"encoding/json"

	"github.com/dapr/dapr/pkg/actors/reminders"
)

// ActorHostedRequest is the request object for checking if an actor is hosted on this instance.
type ActorHostedRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
}

// CreateReminderRequest is the request object to create a new reminder.
type CreateReminderRequest = reminders.CreateReminderRequest

// CreateTimerRequest is the request object to create a new timer.
type CreateTimerRequest = reminders.CreateTimerRequest

// DeleteReminderRequest is the request object for deleting a reminder.
type DeleteReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// DeleteStateRequest is the request object for deleting an actor state.
type DeleteStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
}

// DeleteTimerRequest is a request object for deleting a timer.
type DeleteTimerRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// GetReminderRequest is the request object to get an existing reminder.
type GetReminderRequest struct {
	Name      string
	ActorType string
	ActorID   string
}

// GetStateRequest is the request object for getting actor state.
type GetStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
}

// ActorKey returns the key of the actor for this request.
func (r GetStateRequest) ActorKey() string {
	return r.ActorType + daprSeparator + r.ActorID
}

// ReminderResponse is the payload that is sent to an Actor SDK API for execution.
type ReminderResponse struct {
	Data    any    `json:"data"`
	DueTime string `json:"dueTime"`
	Period  string `json:"period"`
}

// MarshalJSON is a custom JSON marshaler that encodes the data as JSON.
func (r *ReminderResponse) MarshalJSON() ([]byte, error) {
	type responseAlias ReminderResponse
	m := struct {
		Data []byte `json:"data,omitempty"`
		*responseAlias
	}{
		responseAlias: (*responseAlias)(r),
	}

	var err error
	m.Data, err = json.Marshal(r.Data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}

// RenameReminderRequest is the request object for rename a reminder.
type RenameReminderRequest struct {
	OldName   string
	ActorType string
	ActorID   string
	NewName   string
}

// SaveStateRequest is the request object for saving an actor state.
type SaveStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
	Value     any    `json:"value"`
}

// StateResponse is the response returned from getting an actor state.
type StateResponse struct {
	Data []byte `json:"data"`
}

// TimerResponse is the response object send to an Actor SDK API when a timer fires.
type TimerResponse struct {
	Callback string `json:"callback"`
	Data     any    `json:"data"`
	DueTime  string `json:"dueTime"`
	Period   string `json:"period"`
}

// MarshalJSON is a custom JSON marshaler that encodes the data as JSON.
func (t *TimerResponse) MarshalJSON() ([]byte, error) {
	type responseAlias TimerResponse
	m := struct {
		Data []byte `json:"data,omitempty"`
		*responseAlias
	}{
		responseAlias: (*responseAlias)(t),
	}

	var err error
	m.Data, err = json.Marshal(t.Data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(m)
}
