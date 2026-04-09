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

	gate := newConcurrencyGate(3, 1)

	assert.True(t, gate.tryAcquire())
	assert.True(t, gate.tryAcquire())
	assert.True(t, gate.tryAcquire())
	assert.False(t, gate.tryAcquire(), "should not acquire beyond limit")

	gate.release()
	assert.True(t, gate.tryAcquire(), "should acquire after release")
	assert.False(t, gate.tryAcquire())
}

func TestConcurrencyGate_EnqueueDequeue(t *testing.T) {
	t.Parallel()

	gate := newConcurrencyGate(1, 1)

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

func TestConcurrencyGate_RecalculateLocal(t *testing.T) {
	t.Parallel()

	gate := newConcurrencyGate(100, 3)
	assert.Equal(t, uint32(33), gate.localLimit)

	gate.recalculateLocal(5)
	assert.Equal(t, uint32(20), gate.localLimit)

	gate.recalculateLocal(1)
	assert.Equal(t, uint32(100), gate.localLimit)
}

func TestConcurrencyGate_ReleaseNeverNegative(t *testing.T) {
	t.Parallel()

	gate := newConcurrencyGate(5, 1)

	gate.release()
	assert.Equal(t, uint32(0), gate.current)

	gate.release()
	assert.Equal(t, uint32(0), gate.current)
}

func TestUpsertGate(t *testing.T) {
	t.Parallel()

	c := &connections{
		concurrencyGates: make(map[string]*concurrencyGate),
	}

	c.upsertGate("activity", 100, 3)
	require.Contains(t, c.concurrencyGates, "activity")
	assert.Equal(t, uint32(33), c.concurrencyGates["activity"].localLimit)

	c.upsertGate("activity:SendEmail", 10, 3)
	require.Contains(t, c.concurrencyGates, "activity:SendEmail")
	assert.Equal(t, uint32(3), c.concurrencyGates["activity:SendEmail"].localLimit)

	// Update existing gate
	c.upsertGate("activity:SendEmail", 30, 3)
	assert.Equal(t, uint32(10), c.concurrencyGates["activity:SendEmail"].localLimit)
}

func TestGateKeysForTrigger(t *testing.T) {
	t.Parallel()

	c := &connections{
		concurrencyGates: make(map[string]*concurrencyGate),
	}

	actorType := "dapr.internal.default.myapp.activity"

	// No gates configured - should return nil
	req := makeTriggerRequest(actorType, "", "")
	keys := c.gateKeysForTrigger(req)
	assert.Nil(t, keys)

	// Only type-level gate
	c.upsertGate(actorType, 50, 1)
	keys = c.gateKeysForTrigger(req)
	assert.Equal(t, []string{actorType}, keys)

	// Type-level + named gate
	c.upsertGate(actorType+":SendEmail", 5, 1)
	reqWithKey := makeTriggerRequest(actorType, "actor1", "SendEmail")
	keys = c.gateKeysForTrigger(reqWithKey)
	assert.Equal(t, []string{actorType, actorType + ":SendEmail"}, keys)

	// Named gate only (no concurrency key on trigger)
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
