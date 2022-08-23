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

package components

// Type is the component type.
type Type string

const (
	State          Type = "state"
	PubSub         Type = "pubsub"
	InputBinding   Type = "inputbinding"
	OutputBinding  Type = "outputbinding"
	HTTPMiddleware Type = "middleware.http"
	Configuration  Type = "configuration"
	Secret         Type = "secret"
	Lock           Type = "lock"
	NameResolution Type = "nameresolution"
)

// WellKnownTypes is used as a handy way to iterate over all possible component type.
var WellKnownTypes = [9]Type{
	State,
	PubSub,
	InputBinding,
	OutputBinding,
	HTTPMiddleware,
	Configuration,
	Secret,
	Lock,
	NameResolution,
}
