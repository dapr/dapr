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
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops/connections/store"
	loopfake "github.com/dapr/kit/events/loop/fake"
)

func TestLocalLimitFromGlobal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		globalLimit    uint32
		schedulerCount uint32
		schedulerIdx   uint32
		expected       uint32
	}{
		{
			name:           "single scheduler gets full limit",
			globalLimit:    100,
			schedulerCount: 1,
			schedulerIdx:   0,
			expected:       100,
		},
		{
			name:           "zero scheduler count treated as single",
			globalLimit:    100,
			schedulerCount: 0,
			schedulerIdx:   0,
			expected:       100,
		},
		{
			name:           "even division across schedulers",
			globalLimit:    90,
			schedulerCount: 3,
			schedulerIdx:   0,
			expected:       30,
		},
		{
			name:           "remainder distribution global=100 count=3: idx 0 gets base+1",
			globalLimit:    100,
			schedulerCount: 3,
			schedulerIdx:   0,
			expected:       34,
		},
		{
			name:           "remainder distribution global=100 count=3: idx 1 gets base",
			globalLimit:    100,
			schedulerCount: 3,
			schedulerIdx:   1,
			expected:       33,
		},
		{
			name:           "remainder distribution global=100 count=3: idx 2 gets base",
			globalLimit:    100,
			schedulerCount: 3,
			schedulerIdx:   2,
			expected:       33,
		},
		{
			name:           "remainder distribution global=5 count=3: idx 0 gets 2",
			globalLimit:    5,
			schedulerCount: 3,
			schedulerIdx:   0,
			expected:       2,
		},
		{
			name:           "remainder distribution global=5 count=3: idx 1 gets 2",
			globalLimit:    5,
			schedulerCount: 3,
			schedulerIdx:   1,
			expected:       2,
		},
		{
			name:           "remainder distribution global=5 count=3: idx 2 gets 1",
			globalLimit:    5,
			schedulerCount: 3,
			schedulerIdx:   2,
			expected:       1,
		},
		{
			name:           "global limit smaller than scheduler count gets minimum of 1",
			globalLimit:    1,
			schedulerCount: 3,
			schedulerIdx:   0,
			expected:       1,
		},
		{
			name:           "global limit equal to scheduler count",
			globalLimit:    3,
			schedulerCount: 3,
			schedulerIdx:   2,
			expected:       1,
		},
		{
			name:           "large scale scenario: idx 0 takes remainder",
			globalLimit:    100000,
			schedulerCount: 3,
			schedulerIdx:   0,
			expected:       33334,
		},
		{
			name:           "large scale scenario: idx 2 no remainder",
			globalLimit:    100000,
			schedulerCount: 3,
			schedulerIdx:   2,
			expected:       33333,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := localLimitFromGlobal(tc.globalLimit, tc.schedulerCount, tc.schedulerIdx)
			assert.Equal(t, tc.expected, got)
		})
	}
}

// TestLocalLimitFromGlobal_SumEqualsGlobal asserts that summing per-scheduler
// shares yields exactly the global limit when globalLimit >= schedulerCount.
func TestLocalLimitFromGlobal_SumEqualsGlobal(t *testing.T) {
	t.Parallel()

	for _, count := range []uint32{1, 2, 3, 5, 7, 10} {
		for _, global := range []uint32{count, count + 1, count * 3, count*3 + 1, 100} {
			if global < count {
				continue
			}
			var sum uint32
			for idx := range count {
				sum += localLimitFromGlobal(global, count, idx)
			}
			assert.Equal(t, global, sum, "count=%d global=%d", count, global)
		}
	}
}

func TestConcurrencyGate_TryAcquireRelease(t *testing.T) {
	t.Parallel()

	gate := &concurrencyGate{globalLimit: 3}

	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.False(t, gate.tryAcquire(1, 0), "should not acquire beyond limit")

	gate.release()
	assert.True(t, gate.tryAcquire(1, 0), "should acquire after release")
	assert.False(t, gate.tryAcquire(1, 0))
}

func TestConcurrencyGate_DynamicSchedulerCount(t *testing.T) {
	t.Parallel()

	gate := &concurrencyGate{globalLimit: 6}

	// With 1 scheduler, local limit is 6.
	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.True(t, gate.tryAcquire(1, 0))
	assert.False(t, gate.tryAcquire(1, 0))

	// Release all.
	for range 6 {
		gate.release()
	}

	// With 3 schedulers (even split), each gets 2.
	assert.True(t, gate.tryAcquire(3, 0))
	assert.True(t, gate.tryAcquire(3, 0))
	assert.False(t, gate.tryAcquire(3, 0), "should respect new scheduler count")

	// If scheduler count drops back to 1, limit returns to 6.
	assert.True(t, gate.tryAcquire(1, 0))
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

// TestDrainPending_PreservesSeniority asserts that a pending trigger skipped
// by the drain scan (blocked on a secondary gate) keeps its position at the
// front of the queue rather than being rotated behind fresher arrivals.
func TestDrainPending_PreservesSeniority(t *testing.T) {
	t.Parallel()

	const actorType = "dapr.internal.default.myapp.workflow"

	var dispatched []loops.EventStream
	streamLoop := loopfake.New[loops.EventStream]().WithEnqueue(func(e loops.EventStream) {
		dispatched = append(dispatched, e)
	})

	pool := store.New()
	pool.Add(store.Options{
		Loop:       streamLoop,
		ActorTypes: []string{actorType},
	})

	primary := &concurrencyGate{globalLimit: 5}
	named := &concurrencyGate{globalLimit: 1, current: 1}

	c := &connections{
		streamPool: pool,
		concurrencyGates: map[string]*concurrencyGate{
			actorType:          primary,
			actorType + ":act": named,
		},
		streamGateKeys: make(map[uint64][]string),
		schedulerCount: 1,
	}

	elder := makeTriggerRequest(actorType, "a1", "act")
	mid := makeTriggerRequest(actorType, "a2", "")
	fresh := makeTriggerRequest(actorType, "a3", "")

	require.True(t, primary.enqueue(elder))
	require.True(t, primary.enqueue(mid))
	require.True(t, primary.enqueue(fresh))

	c.drainPending(actorType, primary)

	require.Len(t, dispatched, 1)
	assert.Same(t, mid, dispatched[0].(*loops.TriggerRequest))

	require.Equal(t, 2, primary.pendingLen())
	assert.Same(t, elder, primary.dequeue(), "skipped elder must stay at the front of the pending queue")
	assert.Same(t, fresh, primary.dequeue())

	assert.Equal(t, uint32(1), primary.current, "only the dispatched trigger holds the primary gate")
	assert.Equal(t, uint32(1), named.current, "partial acquisition must be rolled back")
}
