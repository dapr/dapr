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

package workflow

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(allgone))
}

// allgone checks a fresh replica gets both the running and the waiting
// workflow turn after every replica is gone, and both workflows complete.
type allgone struct {
	workflow *workflow.Workflow
}

const allgoneAppID = "workflow-allgone"

const allgoneConfig = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowallgone
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`

func (a *allgone) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, allgoneConfig), daprd.WithAppID(allgoneAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *allgone) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	var heldOld, heldNew, ranOld, ranNew atomic.Int64
	release := make(chan struct{})
	registry := func(held, ran *atomic.Int64) *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("hold", func(wctx *task.WorkflowContext) (any, error) {
			// The start returns once the first turn commits, so hold blocks in its second.
			if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !wctx.IsReplaying {
				held.Add(1)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, nil
		}))
		require.NoError(t, r.AddWorkflowN("plain", func(wctx *task.WorkflowContext) (any, error) {
			if !wctx.IsReplaying {
				ran.Add(1)
			}
			return nil, nil
		}))
		return r
	}

	oldClient := a.workflow.BackendClientFor(t, ctx, a.workflow.Dapr(), registry(&heldOld, &ranOld))
	_, err := oldClient.ScheduleNewWorkflow(ctx, "hold",
		api.WithInstanceID("hold"))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), heldOld.Load())
	}, time.Second*10, time.Millisecond*10)

	backlog := func() float64 {
		return a.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:"+allgoneAppID)
	}

	// The only slot is held, so plain's first turn waits. Its start fails
	// when the replica dies, but the turn is already in the scheduler.
	go func() {
		_, _ = oldClient.ScheduleNewWorkflow(ctx, "plain",
			api.WithInstanceID("plain"))
	}()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, backlog(), 0)
	}, time.Second*10, time.Millisecond*10)

	// The only replica dies with one turn running and one waiting.
	a.workflow.Dapr().Kill(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, backlog(), 0)
	}, time.Second*10, time.Millisecond*10)

	fresh := a.workflow.NewDaprd(t, daprd.WithAppID(allgoneAppID), daprd.WithConfigManifests(t, allgoneConfig))
	fresh.Run(t, ctx)
	t.Cleanup(func() { fresh.Cleanup(t) })
	fresh.WaitUntilRunning(t, ctx)
	newClient := a.workflow.BackendClientFor(t, ctx, fresh, registry(&heldNew, &ranNew))

	// Longer: the running turn is delivered again only after the scheduler's
	// reattach grace for the dead replica.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), heldNew.Load())
	}, time.Second*60, time.Millisecond*10)

	close(release)
	for _, id := range []api.InstanceID{"hold", "plain"} {
		meta, err := newClient.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
	assert.Equal(t, int64(0), ranOld.Load())
	assert.Equal(t, int64(1), ranNew.Load(), "plain's waiting turn runs on the fresh replica")
}
