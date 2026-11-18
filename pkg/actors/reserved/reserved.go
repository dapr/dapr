/*
Copyright 2026 The Dapr Authors
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

// Package reserved defines the actor types Dapr reserves for its own internal
// actors, such as the workflow and activity actors. It has no dependencies so
// that every package deciding whether an actor type is internal can share it.
package reserved

import "strings"

const (
	// InternalActorType is the bare reserved actor type.
	InternalActorType = "dapr.internal"
	// InternalActorTypePrefix prefixes every internal actor type, for example
	// "dapr.internal.<namespace>.<appID>.workflow".
	InternalActorTypePrefix = InternalActorType + "."
)

// IsInternalActorType reports whether actorType is reserved for Dapr internal
// actors: the bare "dapr.internal" or any type under "dapr.internal.".
func IsInternalActorType(actorType string) bool {
	return actorType == InternalActorType || strings.HasPrefix(actorType, InternalActorTypePrefix)
}
