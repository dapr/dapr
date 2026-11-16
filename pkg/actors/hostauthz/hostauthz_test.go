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

package hostauthz

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInternalActorType(t *testing.T) {
	tests := map[string]struct {
		actorType, ns, appID string
		internal, owned      bool
	}{
		"user actor type":                                        {"myactor", "default", "app", false, false},
		"user type sharing the dapr prefix":                      {"dapr.internalx.default.app.workflow", "default", "app", false, false},
		"own workflow type":                                      {"dapr.internal.default.app.workflow", "default", "app", true, true},
		"another app's type":                                     {"dapr.internal.default.other.workflow", "default", "app", true, false},
		"another app whose ID shares a prefix":                   {"dapr.internal.default.app2.workflow", "default", "app", true, false},
		"another namespace":                                      {"dapr.internal.prod.app.workflow", "default", "app", true, false},
		"own type in a dotted namespace":                         {"dapr.internal.prod.team.orders.workflow", "prod.team", "orders", true, true},
		"cross-namespace overlap, scoped by namespace elsewhere": {"dapr.internal.prod.team.orders.workflow", "prod", "team", true, true},
		"bare prefix":                                            {"dapr.internal", "default", "app", true, false},
		"no suffix after the app ID":                             {"dapr.internal.default.app", "default", "app", true, true},
		"empty namespace segment":                                {"dapr.internal..app.workflow", "", "app", true, false},
		"too short to name an owner":                             {"dapr.internal.default", "default", "app", true, false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			internal, owned := InternalActorType(tc.actorType, tc.ns, tc.appID)
			assert.Equal(t, tc.internal, internal, "internal")
			assert.Equal(t, tc.owned, owned, "owned")
		})
	}
}
