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

package signed

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	procworkflow "github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(tamperedevent))
}

// tamperedevent verifies that mutating an event byte inside the persisted
// PropagatedHistory state-store row is caught by the load-time re-verification
// path, and the workflow is tombstoned to FAILED. This is the propagation-side
// counterpart to signing/tampered.go.
type tamperedevent struct {
	workflow *procworkflow.Workflow

	childInstanceID atomic.Value // string
}

func (s *tamperedevent) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(2),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *tamperedevent) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)

	// Child blocks on an external event so the orchestrator stays
	// active long enough for us to tamper with the persisted state.
	app1Reg.AddWorkflowN("tamperEvtChild", func(ctx *task.WorkflowContext) (any, error) {
		s.childInstanceID.Store(string(ctx.ID))
		var payload string
		if err := ctx.WaitForSingleEvent("continue", 30*time.Second).Await(&payload); err != nil {
			return nil, err
		}
		return payload, nil
	})

	app0Reg.AddWorkflowN("tamperEvtParent", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallChildWorkflow("tamperEvtChild",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})

	client0 := s.workflow.BackendClient(t, ctx)
	client1 := s.workflow.BackendClientN(t, ctx, 1)

	_, err := client0.ScheduleNewWorkflow(ctx, "tamperEvtParent")
	require.NoError(t, err)

	// Wait until the child has started and persisted IncomingHistory.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		v := s.childInstanceID.Load()
		assert.NotNil(c, v)
		if v != nil {
			assert.NotEmpty(c, v.(string))
		}
	}, 20*time.Second, 10*time.Millisecond)
	childID := s.childInstanceID.Load().(string)

	// Wait for the persisted propagated-history row to appear.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountPropagatedHistoryRows(t, ctx, s.workflow.DB(), childID))
	}, 20*time.Second, 10*time.Millisecond)

	_, err = client1.WaitForWorkflowStart(ctx, api.InstanceID(childID))
	require.NoError(t, err)

	// Flip a byte inside the chunk's first rawEvent. This invalidates the
	// chunk's signature on next load.
	key, ph := fworkflow.ReadPropagatedHistory(t, ctx, s.workflow.DB(), childID)
	require.NotEmpty(t, ph.GetChunks())
	require.NotEmpty(t, ph.GetChunks()[0].GetRawEvents())
	require.NotEmpty(t, ph.GetChunks()[0].GetRawEvents()[0],
		"sanity: chunk's first rawEvent should be non-empty")

	ph.GetChunks()[0].GetRawEvents()[0][0] ^= 0xFF
	fworkflow.WritePropagatedHistory(t, ctx, s.workflow.DB(), key, ph)

	// Restart App1's daprd so the orchestrator actor reloads from the
	// state store and re-runs propagated-history verification.
	s.workflow.DaprN(1).Restart(t, ctx)
	s.workflow.DaprN(1).WaitUntilRunning(t, ctx)

	// Re-register so the worker is connected post-restart.
	client1 = s.workflow.BackendClientN(t, ctx, 1)

	// Trigger child activation by raising the awaited event. The load
	// path runs first; tamper detection trumps the event delivery.
	require.NoError(t, client1.RaiseEvent(ctx, api.InstanceID(childID), "continue", api.WithEventPayload("real-event")))

	// Tampered child workflow must surface as FAILED.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client1.FetchWorkflowMetadata(ctx, api.InstanceID(childID))
		assert.NoError(c, err)
		assert.Equal(c, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus(),
			"tampered propagated-history must tombstone the child workflow")
	}, 20*time.Second, 10*time.Millisecond)
}
