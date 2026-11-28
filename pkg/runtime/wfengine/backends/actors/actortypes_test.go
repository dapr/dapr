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

package actors

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewActorTypes(t *testing.T) {
	types := NewActorTypes("ns", "app")

	assert.Equal(t, ActorTypes{
		Workflow:    "dapr.internal.ns.app.workflow",
		Activity:    "dapr.internal.ns.app.activity",
		Executor:    "dapr.internal.ns.app.executor",
		Retentioner: "dapr.internal.ns.app.retentioner",
	}, types)

	assert.Equal(t, []string{
		"dapr.internal.ns.app.workflow",
		"dapr.internal.ns.app.activity",
		"dapr.internal.ns.app.executor",
		"dapr.internal.ns.app.retentioner",
	}, types.All())
}

// TestNewUsesActorTypes checks that the backend names its actor types with
// NewActorTypes.
func TestNewUsesActorTypes(t *testing.T) {
	abe, err := New(Options{Namespace: "ns", AppID: "app"})
	require.NoError(t, err)
	types := NewActorTypes("ns", "app")
	assert.Equal(t, types.Workflow, abe.workflowActorType)
	assert.Equal(t, types.Activity, abe.activityActorType)
	assert.Equal(t, types.Executor, abe.executorActorType)
	assert.Equal(t, types.Retentioner, abe.retentionerActorType)
}
