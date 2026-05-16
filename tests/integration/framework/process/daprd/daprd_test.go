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

package daprd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
)

func TestActorRuntimeHostsTypes(t *testing.T) {
	tests := map[string]struct {
		actorRuntime *MetadataActorRuntime
		actorTypes   []string
		exp          bool
	}{
		"nil runtime": {
			actorTypes: []string{"workflow"},
		},
		"not ready": {
			actorRuntime: &MetadataActorRuntime{
				RuntimeStatus: rtv1.ActorRuntime_RUNNING.String(),
				HostReady:     false,
				ActiveActors: []*MetadataActorRuntimeActiveActor{
					{Type: "workflow"},
				},
			},
			actorTypes: []string{"workflow"},
		},
		"missing actor type": {
			actorRuntime: &MetadataActorRuntime{
				RuntimeStatus: rtv1.ActorRuntime_RUNNING.String(),
				HostReady:     true,
				ActiveActors: []*MetadataActorRuntimeActiveActor{
					{Type: "workflow"},
				},
			},
			actorTypes: []string{"workflow", "activity"},
		},
		"all actor types hosted": {
			actorRuntime: &MetadataActorRuntime{
				RuntimeStatus: rtv1.ActorRuntime_RUNNING.String(),
				HostReady:     true,
				ActiveActors: []*MetadataActorRuntimeActiveActor{
					{Type: "activity"},
					{Type: "workflow"},
				},
			},
			actorTypes: []string{"workflow", "activity"},
			exp:        true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.exp, actorRuntimeHostsTypes(tc.actorRuntime, tc.actorTypes...))
		})
	}
}
