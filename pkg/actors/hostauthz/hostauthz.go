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

// Package hostauthz decides which app owns a reserved internal actor type.
package hostauthz

import (
	"strings"
)

const internalActorTypePrefix = "dapr.internal."

// InternalActorType reports whether actorType is a reserved internal actor
// type (dapr.internal.*) and, if so, whether it belongs to appID in namespace
// ns, that is whether it is "dapr.internal.<ns>.<appID>" or starts with it
// followed by a dot. The owner is matched as a prefix rather than parsed out
// of the type because a self-hosted namespace may contain dots; an app ID
// cannot, so the match is unambiguous within a namespace. A malformed
// internal type, such as one with an empty segment, is internal and owned by
// no one.
func InternalActorType(actorType, ns, appID string) (internal, owned bool) {
	if actorType != "dapr.internal" && !strings.HasPrefix(actorType, internalActorTypePrefix) {
		return false, false
	}
	if ns == "" || appID == "" {
		return true, false
	}
	owner := internalActorTypePrefix + ns + "." + appID
	return true, actorType == owner || strings.HasPrefix(actorType, owner+".")
}
