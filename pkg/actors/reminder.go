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

// Reminder represents a persisted reminder for a unique actor.
type Reminder struct {
	ActorID        string      `json:"actorID,omitempty"`
	ActorType      string      `json:"actorType,omitempty"`
	Name           string      `json:"name,omitempty"`
	Data           interface{} `json:"data"`
	Period         string      `json:"period"`
	DueTime        string      `json:"dueTime"`
	RegisteredTime string      `json:"registeredTime,omitempty"`
	ExpirationTime string      `json:"expirationTime,omitempty"`
}
