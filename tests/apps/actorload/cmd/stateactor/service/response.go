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

package service

import (
	"encoding/json"
	"io"
)

// ActorResponse is the default response body contract.
type ActorResponse struct {
	Message string `json:"message"`
}

// NewActorResponse creates ActorResponse with the given message.
func NewActorResponse(msg string) ActorResponse {
	return ActorResponse{Message: msg}
}

// Encode serializes ActorResponse to write buffer.
func (e ActorResponse) Encode(w io.Writer) {
	json.NewEncoder(w).Encode(e)
}
