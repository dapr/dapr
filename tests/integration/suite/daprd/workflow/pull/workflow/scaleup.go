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
	"fmt"
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
	suite.Register(new(scaleup))
}

// scaleup checks workflow turns waiting in the scheduler are handed to
// replicas that join later, one per free workflow slot.
type scaleup struct {
	workflow *workflow.Workflow
}

const scaleupAppID = "workflow-scaleup"

const scaleupConfig = `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowscaleup
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`

func (s *scaleup) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, scaleupConfig), daprd.WithAppID(scaleupAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *scaleup) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const n = 3
	var held atomic.Int64
	release := make(chan struct{})
	registry := func() *task.TaskRegistry {
		r := task.NewTaskRegistry()
		require.NoError(t, r.AddWorkflowN("hold", func(wctx *task.WorkflowContext) (any, error) {
			// Blocks in the first workflow turn, so each workflow keeps its slot.
			if !wctx.IsReplaying {
				held.Add(1)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, nil
		}))
		return r
	}

	client := s.workflow.BackendClientFor(t, ctx, s.workflow.Dapr(), registry())

	backlog := func() float64 {
		return s.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_backlog", "app_id:"+scaleupAppID)
	}
	inflight := func() float64 {
		return s.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_inflight", "app_id:"+scaleupAppID)
	}

	// One slot in the fleet: one workflow turn runs, two wait in the scheduler.
	errCh := make(chan error, n)
	ids := make([]api.InstanceID, n)
	for i := range n {
		ids[i] = api.InstanceID(fmt.Sprintf("hold-%d", i))
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, "hold",
				api.WithInstanceID(ids[i]))
			errCh <- err
		}()
	}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), held.Load())
		assert.InDelta(c, 1.0, inflight(), 0)
		assert.InDelta(c, 2.0, backlog(), 0)
	}, time.Second*10, time.Millisecond*10)

	// Each joining replica takes one waiting workflow turn with its one slot. A join
	// moves some workflows to the new replica, which runs their workflow turn again,
	// and a workflow turn runs only once its owner's worker is free, so the workflow turns are
	// counted in the scheduler and not in the app.
	for _, waiting := range []int{1, 0} {
		joined := s.workflow.NewDaprd(t, daprd.WithAppID(scaleupAppID), daprd.WithConfigManifests(t, scaleupConfig))
		joined.Run(t, ctx)
		t.Cleanup(func() { joined.Cleanup(t) })
		joined.WaitUntilRunning(t, ctx)
		s.workflow.BackendClientFor(t, ctx, joined, registry())

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.InDelta(c, float64(n-waiting), inflight(), 0)
			assert.InDelta(c, float64(waiting), backlog(), 0)
		}, time.Second*10, time.Millisecond*10)
	}

	close(release)
	for range n {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 10):
			require.Fail(t, "a start did not return")
		}
	}
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
	assert.GreaterOrEqual(t, held.Load(), int64(n))
}
