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

	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend/local"
)

func Test_activityExecutions(t *testing.T) {
	t.Parallel()

	a := newActivityExecutions()
	key := activityExecutionKey("wf1", 3)

	assert.False(t, a.heldFor("wf1", 3))

	release := a.add(key)
	assert.True(t, a.heldFor("wf1", 3))
	assert.False(t, a.heldFor("wf1", 4))
	assert.False(t, a.heldFor("wf2", 3))

	// Overlapping registrations for the same execution key are counted.
	release2 := a.add(key)
	release()
	assert.True(t, a.heldFor("wf1", 3))
	release2()
	assert.False(t, a.heldFor("wf1", 3))

	// The release is idempotent: a delivery and a deregistration both firing
	// must not underflow another registration's count.
	release3 := a.add(key)
	release()
	release2()
	assert.True(t, a.heldFor("wf1", 3))
	release3()
	release3()
	assert.False(t, a.heldFor("wf1", 3))
}

// Test_onActivityCompletionMirrorsHeld verifies the registration mirror the
// stale-claim eviction oracle relies on: an activity work item reads as held
// exactly while its completion registration is outstanding.
func Test_onActivityCompletionMirrorsHeld(t *testing.T) {
	t.Parallel()

	abe := &Actors{
		pendingTasksBackend: local.NewTasksBackend(),
		activityExecs:       newActivityExecutions(),
	}

	req := &protos.ActivityRequest{
		WorkflowInstance: &protos.WorkflowInstance{InstanceId: "wf1"},
		TaskId:           3,
	}

	delivered := make(chan *protos.ActivityResponse, 1)
	dereg := abe.OnActivityCompletion(req, func(resp *protos.ActivityResponse, err error) {
		delivered <- resp
	})
	assert.True(t, abe.ActivityExecutionHeld("wf1", 3))

	// Delivery releases the registration and reaches the callback.
	require.NoError(t, abe.CompleteActivityTask(t.Context(), &protos.ActivityResponse{InstanceId: "wf1", TaskId: 3}))
	assert.False(t, abe.ActivityExecutionHeld("wf1", 3))
	select {
	case resp := <-delivered:
		assert.Equal(t, "wf1", resp.GetInstanceId())
	default:
		t.Fatal("the completion must reach the callback")
	}

	// Deregistration alone also releases.
	dereg2 := abe.OnActivityCompletion(req, func(*protos.ActivityResponse, error) {})
	assert.True(t, abe.ActivityExecutionHeld("wf1", 3))
	dereg2()
	assert.False(t, abe.ActivityExecutionHeld("wf1", 3))

	dereg()
}
