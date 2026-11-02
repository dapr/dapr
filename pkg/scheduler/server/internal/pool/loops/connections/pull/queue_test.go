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

package pull

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/pool/loops"
)

func TestQueue_InstanceKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "wf-1", instanceKey("wf-1::3::0"))
	assert.Equal(t, "a::b", instanceKey("a::b::3::0"), "instance IDs may contain the separator")
	assert.Equal(t, "plain", instanceKey("plain"))
	assert.Equal(t, "one::two", instanceKey("one::two"), "fewer than two separators is not an activity ID")
}

func TestQueue_TryDispatchOneRotates(t *testing.T) {
	t.Parallel()
	q := newQueue(10)

	mk := func(id string) *loops.TriggerRequest {
		return &loops.TriggerRequest{Job: &internalsv1pb.JobEvent{Metadata: &schedulerv1pb.JobMetadata{
			Target: &schedulerv1pb.JobTargetMetadata{Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{Type: testActorType, Id: id},
			}},
		}}}
	}
	a1, a2, a3, b1, c1 := mk("a::1::0"), mk("a::2::0"), mk("a::3::0"), mk("b::1::0"), mk("c::1::0")
	for _, r := range []*loops.TriggerRequest{a1, a2, a3, b1, c1} {
		require.True(t, q.enqueue(r, false))
	}
	require.Equal(t, 5, q.len())

	var order []*loops.TriggerRequest
	accept := func(r *loops.TriggerRequest) bool {
		order = append(order, r)
		return true
	}
	for q.tryDispatchOne(accept) {
	}
	assert.Equal(t, []*loops.TriggerRequest{a1, b1, c1, a2, a3}, order)
	assert.Equal(t, 0, q.len())
	assert.Empty(t, q.ring)

	// A rejected head is skipped for this rotation; another instance's head
	// may still go.
	require.True(t, q.enqueue(a1, false))
	require.True(t, q.enqueue(b1, false))
	order = nil
	require.True(t, q.tryDispatchOne(func(r *loops.TriggerRequest) bool {
		order = append(order, r)
		return r == b1
	}))
	assert.Equal(t, []*loops.TriggerRequest{a1, b1}, order)
	assert.Equal(t, 1, q.len())
	assert.False(t, q.tryDispatchOne(func(*loops.TriggerRequest) bool { return false }))
	assert.Equal(t, 1, q.len())
}

func TestQueue_DrainAll(t *testing.T) {
	t.Parallel()
	q := newQueue(10)
	h := newHarness(t, 0, 0)
	req := h.trigger("wf::1::0", "act")
	require.True(t, q.enqueue(req, false))
	var drained []*loops.TriggerRequest
	q.drainAll(func(r *loops.TriggerRequest) { drained = append(drained, r) })
	assert.Equal(t, []*loops.TriggerRequest{req}, drained)
	assert.Equal(t, 0, q.len())
}

func TestQueue_Bound(t *testing.T) {
	t.Parallel()
	q := newQueue(1)
	h := newHarness(t, 0, 0)
	require.True(t, q.enqueue(h.trigger("wf::1::0", "act"), false))
	assert.False(t, q.enqueue(h.trigger("wf::2::0", "act"), false), "a full queue refuses a new trigger")
	assert.True(t, q.enqueue(h.trigger("wf::3::0", "act"), true), "an admitted trigger parks past the bound")
	assert.Equal(t, 2, q.len())
}

func TestQueue_GroupsByInstanceAndName(t *testing.T) {
	t.Parallel()
	h := newHarness(t, 0, 0)
	assert.Equal(t, groupKey(h.trigger("wf::1::0", "act")), groupKey(h.trigger("wf::2::0", "act")))
	assert.NotEqual(t, groupKey(h.trigger("wf::1::0", "act")), groupKey(h.trigger("wf::2::0", "other")), "another activity name is another group")
	assert.NotEqual(t, groupKey(h.trigger("wf-a::1::0", "act")), groupKey(h.trigger("wf-b::1::0", "act")), "another instance is another group")
}
