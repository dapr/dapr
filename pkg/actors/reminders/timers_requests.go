/*
Copyright 2023 The Dapr Authors
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

package reminders

import (
	"encoding/json"
)

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
