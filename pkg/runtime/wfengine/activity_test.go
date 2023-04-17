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
package wfengine

import (
	"context"
	"testing"

	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/pkg/actors"
)

// TestDedupeActivityInvocation executes the activity actor invocation path for the same (simulated) workflow
// twice to verify that the second call fails with a duplication invocation error.
func TestDedupeActivityInvocation(t *testing.T) {
	// We do just enough to get the activity actor properly initialized
	ctx := context.Background()
	_, engine := startEngine(ctx, t, task.NewTaskRegistry())
	engine.config = getConfig()

	// Get a reference to the activity actor so we can invoke it directly, without going through a workflow.
	activityActor := engine.InternalActors()[engine.config.ActivityActorType]

	generation := uint64(0)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			generation++

			// Generate the same invocation payload that a workflow would generate for a real activity call.
			data, err := actors.EncodeInternalActorData(ActivityRequest{
				HistoryEvent: nil,
				Generation:   generation,
			})
			assert.NoError(t, err)

			// The first call should succeed (though nothing will get executed since this actor doesn't actually exist)
			_, err = activityActor.InvokeMethod(ctx, "test123", "Execute", data)
			assert.NoError(t, err)

			// The second call should fail with a duplicate invocation error
			_, err = activityActor.InvokeMethod(ctx, "test123", "Execute", data)
			assert.ErrorIs(t, err, ErrDuplicateInvocation)

			// The third call, with an updated generation ID, should succeed
			generation++
			data, err = actors.EncodeInternalActorData(ActivityRequest{
				HistoryEvent: nil,
				Generation:   generation,
			})
			assert.NoError(t, err)
			_, err = activityActor.InvokeMethod(ctx, "test123", "Execute", data)
			assert.NoError(t, err)
		})
	}
}
