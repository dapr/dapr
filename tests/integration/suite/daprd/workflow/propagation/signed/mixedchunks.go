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
	suite.Register(new(mixedchunks))
}

// mixedchunks verifies that in a multi-chunk lineage payload the leaf
// rejects the WHOLE propagated history when ANY upstream chunk fails
// verification — the immediate sender's chunk being valid is not enough.
type mixedchunks struct {
	workflow *procworkflow.Workflow

	leafInstanceID atomic.Value // string
}

func (s *mixedchunks) Setup(t *testing.T) []framework.Option {
	s.workflow = procworkflow.New(t,
		procworkflow.WithDaprds(3),
		procworkflow.WithMTLS(t),
	)
	return []framework.Option{framework.WithProcesses(s.workflow)}
}

func (s *mixedchunks) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	app0Reg := s.workflow.Registry()
	app1Reg := s.workflow.RegistryN(1)
	app2Reg := s.workflow.RegistryN(2)

	app0Reg.AddWorkflowN("mixedRoot", func(ctx *task.WorkflowContext) (any, error) {
		var r string
		return r, ctx.CallChildWorkflow("mixedMiddle",
			task.WithChildWorkflowAppID(s.workflow.DaprN(1).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&r)
	})

	app1Reg.AddWorkflowN("mixedMiddle", func(ctx *task.WorkflowContext) (any, error) {
		var r string
		return r, ctx.CallChildWorkflow("mixedLeaf",
			task.WithChildWorkflowAppID(s.workflow.DaprN(2).AppID()),
			task.WithHistoryPropagation(api.PropagateLineage()),
		).Await(&r)
	})

	app2Reg.AddWorkflowN("mixedLeaf", func(ctx *task.WorkflowContext) (any, error) {
		s.leafInstanceID.Store(string(ctx.ID))
		var p string
		if err := ctx.WaitForSingleEvent("continue", 30*time.Second).Await(&p); err != nil {
			return nil, err
		}
		return p, nil
	})

	client0 := s.workflow.BackendClient(t, ctx)
	s.workflow.BackendClientN(t, ctx, 1)
	client2 := s.workflow.BackendClientN(t, ctx, 2)

	_, err := client0.ScheduleNewWorkflow(ctx, "mixedRoot")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		v := s.leafInstanceID.Load()
		assert.NotNil(c, v)
		if v != nil {
			assert.NotEmpty(c, v.(string))
		}
	}, 20*time.Second, 10*time.Millisecond)
	leafID := s.leafInstanceID.Load().(string)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountPropagatedHistoryRows(t, ctx, s.workflow.DB(), leafID))
	}, 20*time.Second, 10*time.Millisecond)

	_, err = client2.WaitForWorkflowStart(ctx, api.InstanceID(leafID))
	require.NoError(t, err)

	// Tamper App0's chunk only. App1's chunk remains valid. The verifier
	// must reject the whole payload because not every chunk verifies.
	key, ph := fworkflow.ReadPropagatedHistory(t, ctx, s.workflow.DB(), leafID)
	require.GreaterOrEqual(t, len(ph.GetChunks()), 2,
		"leaf payload should carry at least App0 and App1 chunks under lineage")

	app0AppID := s.workflow.Dapr().AppID()
	var tampered bool
	for _, chunk := range ph.GetChunks() {
		if chunk.GetAppId() == app0AppID {
			require.NotEmpty(t, chunk.GetRawSignatures())
			chunk.RawSignatures = nil
			tampered = true
			break
		}
	}
	require.True(t, tampered, "expected to find App0's chunk to tamper")
	fworkflow.WritePropagatedHistory(t, ctx, s.workflow.DB(), key, ph)

	s.workflow.DaprN(2).Restart(t, ctx)
	s.workflow.DaprN(2).WaitUntilRunning(t, ctx)
	client2 = s.workflow.BackendClientN(t, ctx, 2)

	require.NoError(t, client2.RaiseEvent(ctx, api.InstanceID(leafID), "continue", api.WithEventPayload("real-event")))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := client2.FetchWorkflowMetadata(ctx, api.InstanceID(leafID))
		assert.NoError(c, err)
		assert.Equal(c, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus(),
			"any tampered chunk in a multi-chunk lineage must tombstone the receiver")
	}, 20*time.Second, 10*time.Millisecond)
}
