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
	suite.Register(new(workerdisconnect))
}

// workerdisconnect checks a replica whose worker went away offers no workflow
// slots, so workflow turns wait for the replica that still has a worker.
type workerdisconnect struct {
	workflow *workflow.Workflow
}

const workerdisconnectAppID = "workflow-workerdisconnect"

func (w *workerdisconnect) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowworkerdisconnect
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`
	w.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(workerdisconnectAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(workerdisconnectAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workerdisconnect) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	var held, ran [2]atomic.Int64
	release := make(chan struct{})
	for i := range 2 {
		require.NoError(t, w.workflow.RegistryN(i).AddWorkflowN("hold", func(wctx *task.WorkflowContext) (any, error) {
			// The start returns once the first turn commits, so hold blocks in its second.
			if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !wctx.IsReplaying {
				held[i].Add(1)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, nil
		}))
		require.NoError(t, w.workflow.RegistryN(i).AddWorkflowN("plain", func(wctx *task.WorkflowContext) (any, error) {
			if !wctx.IsReplaying {
				ran[i].Add(1)
			}
			return nil, nil
		}))
	}

	client := w.workflow.BackendClientN(t, ctx, 0)
	workerCtx, stopWorker := context.WithCancel(ctx)
	w.workflow.BackendClientN(t, workerCtx, 1)

	// Replica 1 loses its worker: its workflow actor types unregister and its
	// scheduler stream reconnects without them.
	stopWorker()
	w.workflow.WaitForNoConnectedWorkersN(t, ctx, 1)

	hold, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), held[0].Load())
	}, time.Second*10, time.Millisecond*10)
	w.workflow.Scheduler().WaitUntilPullInflight(t, ctx, 1, "app_id:"+workerdisconnectAppID)

	// Replica 0's only slot is held, so plain's first turn waits even though
	// replica 1 is idle.
	errCh := make(chan error, 1)
	go func() {
		_, err := client.ScheduleNewWorkflow(ctx, "plain",
			api.WithInstanceID("plain"))
		errCh <- err
	}()
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 1.0, w.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:"+workerdisconnectAppID), 0)
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int64(0), ran[0].Load()+ran[1].Load(), "plain must wait for replica 0's slot")

	close(release)
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		require.Fail(t, "plain did not start")
	}
	for _, id := range []api.InstanceID{hold, "plain"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
	assert.Equal(t, int64(1), ran[0].Load())
	assert.Equal(t, int64(0), held[1].Load()+ran[1].Load(), "a replica without a worker must run no workflow turns")
}
