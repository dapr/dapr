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

package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	actorapi "github.com/dapr/dapr/pkg/actors/api"
	remindersfake "github.com/dapr/dapr/pkg/actors/reminders/fake"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
)

func Test_reapResolvedEscalation(t *testing.T) {
	t.Parallel()
	const instanceID = "wf-reap-escalation"

	t.Run("an unresolved task keeps its escalated reminder", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		h.orch.reapResolvedEscalation(t.Context(), h.orch.state.History[1])
		assert.NotContains(t, h.snapshotOps(), "delete:run-activity",
			"an escalated reminder for a still-unresolved task is the recovery itself")
	})

	t.Run("a task resolved during the escalation reaps the reminder", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		taskScheduled := h.orch.state.History[1]
		h.orch.state.AddToHistory(taskCompletedEvent(7))
		h.orch.reapResolvedEscalation(t.Context(), taskScheduled)
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Contains(c, h.snapshotOps(), "delete:run-activity",
				"a reminder for a resolved task has no deleter and its fire re-runs the body")
		}, time.Second*5, time.Millisecond*5)
	})
}

func Test_reapEscalatedCompletions(t *testing.T) {
	t.Parallel()
	const instanceID = "wf-reap-completion"

	t.Run("a committed resolution reaps its escalated reminder", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		h.orch.janitorRedispatchedGen = h.orch.state.Generation
		h.orch.janitorEscalated = map[int32]*backend.HistoryEvent{7: h.orch.state.History[1]}
		h.orch.state.AddToHistory(taskCompletedEvent(7))

		h.orch.reapEscalatedCompletions(h.orch.state)
		assert.Empty(t, h.orch.janitorEscalated, "a reaped task must not be reaped again")
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Contains(c, h.snapshotOps(), "delete:run-activity")
		}, time.Second*5, time.Millisecond*5)
	})

	t.Run("an unresolved task keeps its mark and reminder", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		h.orch.janitorRedispatchedGen = h.orch.state.Generation
		h.orch.janitorEscalated = map[int32]*backend.HistoryEvent{7: h.orch.state.History[1]}

		h.orch.reapEscalatedCompletions(h.orch.state)
		assert.Len(t, h.orch.janitorEscalated, 1)
		assert.NotContains(t, h.snapshotOps(), "delete:run-activity")
	})

	t.Run("a generation change voids the marks without reaping", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)
		h.orch.janitorRedispatchedGen = h.orch.state.Generation
		h.orch.janitorEscalated = map[int32]*backend.HistoryEvent{7: h.orch.state.History[1]}
		h.orch.state.AddToHistory(taskCompletedEvent(7))
		h.orch.state.Generation++

		h.orch.reapEscalatedCompletions(h.orch.state)
		assert.Empty(t, h.orch.janitorEscalated,
			"stale-generation marks are void: their reminder identity is shared with the new generation")
		assert.NotContains(t, h.snapshotOps(), "delete:run-activity",
			"a stale-generation mark must not delete a reminder the new generation may own")
	})

	t.Run("a cross-app resolution reaps on the remote activity actor type", func(t *testing.T) {
		t.Parallel()
		h := newWakeHarness(t, instanceID, true)
		h.primeRunning(t, instanceID, 7)

		target := "otherapp"
		scheduled := h.orch.state.History[1]
		scheduled.Router = &protos.TaskRouter{SourceAppID: "testapp", TargetAppID: &target}
		h.orch.janitorRedispatchedGen = h.orch.state.Generation
		h.orch.janitorEscalated = map[int32]*backend.HistoryEvent{7: scheduled}
		h.orch.state.AddToHistory(taskCompletedEvent(7))

		reqCh := make(chan *actorapi.DeleteReminderRequest, 1)
		h.fact.reminders = remindersfake.New().WithDelete(func(_ context.Context, req *actorapi.DeleteReminderRequest) error {
			reqCh <- req
			return nil
		})

		h.orch.reapEscalatedCompletions(h.orch.state)

		select {
		case req := <-reqCh:
			assert.Equal(t, "run-activity", req.Name)
			assert.Equal(t, "dapr.internal.default.otherapp.activity", req.ActorType,
				"a cross-app activity's reminder lives on the remote app's actor type")
			assert.Equal(t, instanceID+"::7", req.ActorID)
		case <-time.After(time.Second * 5):
			require.Fail(t, "the reap delete never reached the reminder store")
		}
	})
}
