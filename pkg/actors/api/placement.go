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

// LookupActorRequest is the request for LookupActor.
type LookupActorRequest struct {
	ActorType string
	ActorID   string
	NoCache   bool
}

// ActorKey returns the key for the actor, which is "type/id".
func (lar LookupActorRequest) ActorKey() string {
	return lar.ActorType + "/" + lar.ActorID
}

// LookupActorResponse is the response from LookupActor.
type LookupActorResponse struct {
	Address string
	AppID   string
	Local   bool
}
