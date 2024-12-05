/*
Copyright 2024 The Dapr Authors
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

package api

import (
	"encoding/json"
)

const (
	DaprSeparator = "||"
)

// ActorHostedRequest is the request object for checking if an actor is hosted on this instance.
type ActorHostedRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
}

// ActorKey returns the key of the actor for this request.
func (r ActorHostedRequest) ActorKey() string {
	return r.ActorType + DaprSeparator + r.ActorID
}

// DeleteStateRequest is the request object for deleting an actor state.
type DeleteStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
}

// ActorKey returns the key of the actor for this request.
func (r DeleteStateRequest) ActorKey() string {
	return r.ActorType + DaprSeparator + r.ActorID
}

// GetStateRequest is the request object for getting actor state.
type GetStateRequest struct {
	ActorID   string `json:"actorId"`
	ActorType string `json:"actorType"`
	Key       string `json:"key"`
}

// ActorKey returns the key of the actor for this request.
func (r GetStateRequest) ActorKey() string {
	return r.ActorType + DaprSeparator + r.ActorID
}

// GetBulkStateRequest is the request object for getting bulk actor state.
type GetBulkStateRequest struct {
	ActorID   string   `json:"actorId"`
	ActorType string   `json:"actorType"`
	Keys      []string `json:"keys"`
}

// ActorKey returns the key of the actor for this request.
func (r GetBulkStateRequest) ActorKey() string {
	return r.ActorType + DaprSeparator + r.ActorID
}

// ReminderResponse is the payload that is sent to an Actor SDK API for execution.
type ReminderResponse struct {
	Data    any    `json:"data"`
	DueTime string `json:"dueTime"`
	Period  string `json:"period"`
}

// MarshalJSON is a custom JSON marshaler that encodes the data as JSON.
// Actor SDKs expect "data" to be a base64-encoded message with the JSON representation of the data, so this makes sure that happens.
// This method implements the json.Marshaler interface.
func (r *ReminderResponse) MarshalJSON() ([]byte, error) {
	type responseAlias ReminderResponse
	m := struct {
		Data json.RawMessage `json:"data,omitempty"`
		*responseAlias
	}{
		responseAlias: (*responseAlias)(r),
	}

	if raw, ok := r.Data.(json.RawMessage); ok {
		m.Data = raw
	} else {
		var err error
		m.Data, err = json.Marshal(r.Data)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(m)
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
	Data     []byte            `json:"data"`
	Metadata map[string]string `json:"metadata"`
}

// BulkStateResponse is the response returned from getting an actor state in bulk.
// It's a map where the key is the key of the state, and the value is the value as byte slice.
type BulkStateResponse map[string][]byte

// TimerResponse is the response object send to an Actor SDK API when a timer fires.
type TimerResponse struct {
	Callback string `json:"callback"`
	Data     any    `json:"data"`
	DueTime  string `json:"dueTime"`
	Period   string `json:"period"`
}

// MarshalJSON is a custom JSON marshaler that encodes the data as JSON.
// Actor SDKs expect "data" to be a base64-encoded message with the JSON representation of the data, so this makes sure that happens.
// This method implements the json.Marshaler interface.
func (t *TimerResponse) MarshalJSON() ([]byte, error) {
	type responseAlias TimerResponse
	m := struct {
		Data any `json:"data,omitempty"`
		*responseAlias
	}{
		responseAlias: (*responseAlias)(t),
	}

	m.Data = t.Data
	return json.Marshal(m)
}
