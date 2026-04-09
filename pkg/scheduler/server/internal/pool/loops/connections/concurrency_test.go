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

package connections

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func TestLocalLimitFromGlobal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		globalLimit    uint32
		schedulerCount uint32
		expected       uint32
	}{
		{
			name:           "single scheduler gets full limit",
			globalLimit:    100,
			schedulerCount: 1,
			expected:       100,
		},
		{
			name:           "zero scheduler count treated as single",
			globalLimit:    100,
			schedulerCount: 0,
			expected:       100,
		},
		{
			name:           "even division across schedulers",
			globalLimit:    90,
			schedulerCount: 3,
			expected:       30,
		},
		{
			name:           "floor division ensures max is not exceeded",
			globalLimit:    100,
			schedulerCount: 3,
			expected:       33,
		},
		{
			name:           "global limit smaller than scheduler count gets minimum of 1",
			globalLimit:    1,
			schedulerCount: 3,
			expected:       1,
		},
		{
			name:           "global limit equal to scheduler count",
			globalLimit:    3,
			schedulerCount: 3,
			expected:       1,
		},
		{
			name:           "large scale scenario",
			globalLimit:    100000,
			schedulerCount: 3,
			expected:       33333,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := localLimitFromGlobal(tc.globalLimit, tc.schedulerCount)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestConcurrencyGate_TryAcquireRelease(t *testing.T) {
	t.Parallel()

	gate := &concurrencyGate{globalLimit: 3}

	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.False(t, gate.tryAcquire(1), "should not acquire beyond limit")

	gate.release()
	assert.True(t, gate.tryAcquire(1), "should acquire after release")
	assert.False(t, gate.tryAcquire(1))
}

func TestConcurrencyGate_DynamicSchedulerCount(t *testing.T) {
	t.Parallel()

	gate := &concurrencyGate{globalLimit: 6}

	// With 1 scheduler, local limit is 6.
	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.True(t, gate.tryAcquire(1))
	assert.False(t, gate.tryAcquire(1))

	// Release all.
	for range 6 {
		gate.release()
	}

	// With 3 schedulers, local limit is 2.
	assert.True(t, gate.tryAcquire(3))
	assert.True(t, gate.tryAcquire(3))
	assert.False(t, gate.tryAcquire(3), "should respect new scheduler count")

	// If scheduler count drops back to 1, limit returns to 6.
	assert.True(t, gate.tryAcquire(1))
}

func TestConcurrencyGate_EnqueueDequeue(t *testing.T) {
	t.Parallel()

	gate := &concurrencyGate{globalLimit: 1}

	assert.Nil(t, gate.dequeue(), "empty queue returns nil")

	req1 := &loops.TriggerRequest{}
	req2 := &loops.TriggerRequest{}

	gate.enqueue(req1)
	gate.enqueue(req2)
	assert.Equal(t, 2, gate.pendingLen())

	got := gate.dequeue()
	require.NotNil(t, got)
	assert.Same(t, req1, got, "FIFO order")

	got = gate.dequeue()
	require.NotNil(t, got)
	assert.Same(t, req2, got)

	assert.Nil(t, gate.dequeue())
	assert.Equal(t, 0, gate.pendingLen())
}

func TestConcurrencyGate_ReleaseNeverNegative(t *testing.T) {
	t.Parallel()

	gate := &concurrencyGate{globalLimit: 5}

	gate.release()
	assert.Equal(t, uint32(0), gate.current)

	gate.release()
	assert.Equal(t, uint32(0), gate.current)
}

func TestGateKeysForTrigger(t *testing.T) {
	t.Parallel()

	c := &connections{
		concurrencyGates: make(map[string]*concurrencyGate),
	}

	actorType := "dapr.internal.default.myapp.activity"

	req := makeTriggerRequest(actorType, "", "")
	keys := c.gateKeysForTrigger(req)
	assert.Nil(t, keys)

	c.concurrencyGates[actorType] = &concurrencyGate{globalLimit: 50}
	keys = c.gateKeysForTrigger(req)
	assert.Equal(t, []string{actorType}, keys)

	c.concurrencyGates[actorType+":SendEmail"] = &concurrencyGate{globalLimit: 5}
	reqWithKey := makeTriggerRequest(actorType, "actor1", "SendEmail")
	keys = c.gateKeysForTrigger(reqWithKey)
	assert.Equal(t, []string{actorType, actorType + ":SendEmail"}, keys)

	reqNoKey := makeTriggerRequest(actorType, "actor1", "")
	keys = c.gateKeysForTrigger(reqNoKey)
	assert.Equal(t, []string{actorType}, keys)
}

func makeTriggerRequest(actorType, actorID, concurrencyKey string) *loops.TriggerRequest {
	meta := &schedulerv1pb.JobMetadata{
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{
					Type: actorType,
					Id:   actorID,
				},
			},
		},
	}
	if concurrencyKey != "" {
		meta.ConcurrencyKey = &concurrencyKey
	}
	return &loops.TriggerRequest{
		Job: &internalsv1pb.JobEvent{
			Metadata: meta,
		},
	}
}
