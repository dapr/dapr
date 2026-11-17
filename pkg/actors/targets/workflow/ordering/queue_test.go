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

package ordering

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/durabletask-go/api/protos"
)

const (
	cont   = protos.OrderingFailurePolicy_ORDERING_FAILURE_POLICY_CONTINUE
	block  = protos.OrderingFailurePolicy_ORDERING_FAILURE_POLICY_BLOCK
	cancel = protos.OrderingFailurePolicy_ORDERING_FAILURE_POLICY_CANCEL
)

func run(id string, p protos.OrderingFailurePolicy) Entry {
	return Entry{InstanceID: id, CreateExecutionID: "1", Policy: p}
}

// add enqueues entries and returns the heads to notify.
func (q *queue) add(t *testing.T, es ...Entry) []*head {
	t.Helper()
	var hs []*head
	for _, e := range es {
		existing, h := q.enqueue(e)
		require.False(t, existing, e.InstanceID)
		if h != nil {
			hs = append(hs, h)
		}
	}
	return hs
}

func headOf(q *queue) string {
	if q.Head == nil {
		return ""
	}
	if q.Head.Cancel {
		return "cancel:" + q.Head.InstanceID
	}
	return q.Head.InstanceID
}

func lastOf(q *queue) string {
	if q.Last == nil {
		return ""
	}
	return q.Last.InstanceID
}

func TestQueuePolicies(t *testing.T) {
	t.Run("every policy runs after a success", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", block), run("s3", cancel))
		for _, id := range []string{"s1", "s2", "s3"} {
			assert.Equal(t, id, headOf(&q))
			q.done(run(id, cont), OutcomeSucceeded)
		}
		assert.True(t, q.empty(), "an idle key with no failure keeps no row")
	})

	t.Run("continue runs after a failure", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", cont))
		_, h := q.done(run("s1", cont), OutcomeFailed)
		assert.Equal(t, "s2", h.InstanceID)
		assert.Equal(t, "s1", h.After.InstanceID)
		q.done(run("s2", cont), OutcomeSucceeded)
		assert.True(t, q.empty())
	})

	t.Run("block waits for a retry in place", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", block), run("s3", cont))
		_, h := q.done(run("s1", cont), OutcomeFailed)
		assert.Nil(t, h)
		assert.Equal(t, "s1", lastOf(&q))

		retry := Entry{InstanceID: "s1", CreateExecutionID: "2"}
		_, hs := q.enqueue(retry)
		assert.Equal(t, "s1", hs.InstanceID, "the retry jumps the blocked queue")
		assert.Equal(t, []Entry{run("s2", block), run("s3", cont)}, q.Queue)

		_, h = q.done(retry, OutcomeSucceeded)
		assert.Equal(t, "s2", h.InstanceID)
	})

	t.Run("block waits for a purge of any run of the failure", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", block))
		q.done(run("s1", cont), OutcomeFailed)
		_, h := q.done(Entry{InstanceID: "s1", CreateExecutionID: "9"}, OutcomePurged)
		assert.Equal(t, "s2", h.InstanceID)
	})

	t.Run("a failure is remembered for later arrivals", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont))
		q.done(run("s1", cont), OutcomeFailed)
		assert.False(t, q.empty())
		assert.Empty(t, q.add(t, run("s2", block), run("s3", cont)), "s3 queues behind the blocked s2")
		_, h := q.done(run("s2", block), OutcomeSkipped)
		assert.Equal(t, "s3", h.InstanceID, "removing the blocked s2 lets s3 continue")
	})

	t.Run("cancel cascades and names the original failure", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", cancel), run("s3", cancel), run("s4", cont))
		_, h := q.done(run("s1", cont), OutcomeFailed)
		for _, id := range []string{"s2", "s3"} {
			assert.Equal(t, "cancel:"+id, headOf(&q))
			assert.Equal(t, "s1", h.After.InstanceID)
			_, h = q.done(run(id, cancel), OutcomeSkipped)
		}
		assert.Equal(t, "s4", headOf(&q))
		assert.Equal(t, "s1", h.After.InstanceID)
	})

	t.Run("a repeat enqueue of a cancel head never releases it", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", cancel))
		q.done(run("s1", cont), OutcomeFailed)
		existing, h := q.enqueue(run("s2", cancel))
		assert.True(t, existing)
		assert.True(t, h.Cancel)
	})

	t.Run("a cancelled waiter is not the failure", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", cancel), run("s3", block))
		q.done(run("s1", cont), OutcomeFailed)
		q.done(run("s2", cancel), OutcomeSkipped)
		assert.Equal(t, "s1", lastOf(&q))

		changed, _ := q.done(run("s2", cancel), OutcomePurged)
		assert.False(t, changed, "purging the cancelled s2 does not unblock s3")
	})

	t.Run("a force purge of a cancel head keeps the failure", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", cancel), run("s3", block))
		q.done(run("s1", cont), OutcomeFailed)
		_, h := q.done(run("s2", cancel), OutcomePurged)
		assert.Nil(t, h)
		assert.Equal(t, "s1", lastOf(&q))
	})

	t.Run("purging a retry in place skips its failure", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", block))
		q.done(run("s1", cont), OutcomeFailed)
		retry := Entry{InstanceID: "s1", CreateExecutionID: "2"}
		q.enqueue(retry)
		_, h := q.done(retry, OutcomePurged)
		assert.Equal(t, "s2", h.InstanceID)
	})

	t.Run("strays change nothing and re-notify the head", func(t *testing.T) {
		var q queue
		q.add(t, run("s1", cont), run("s2", block), run("s3", cont))
		q.done(run("s1", cont), OutcomeFailed)

		existing, h := q.enqueue(run("s1", cont))
		assert.True(t, existing, "a late enqueue of the failure itself")
		assert.Nil(t, h)
		for _, o := range []Outcome{OutcomeFailed, OutcomeSkipped, OutcomeSucceeded} {
			changed, _ := q.done(run("s1", cont), o)
			assert.False(t, changed, o)
		}
		assert.Equal(t, "s1", lastOf(&q))

		q.done(run("s1", cont), OutcomePurged)
		changed, h := q.done(run("old", cont), OutcomeFailed)
		assert.False(t, changed)
		assert.Equal(t, "s2", h.InstanceID, "an unknown Done re-notifies the current head")
	})
}
