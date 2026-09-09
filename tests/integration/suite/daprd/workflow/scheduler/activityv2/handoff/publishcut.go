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

package handoff

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
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(publishcut))
}

type publishcut struct {
	workflow *workflow.Workflow
	joiner   *daprd.Daprd
}

func (p *publishcut) Setup(t *testing.T) []framework.Option {
	fp := []daprd.Option{
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
	}
	p.workflow = workflow.New(t, workflow.WithDaprdOptions(0, fp...))
	p.joiner = daprd.New(t, append([]daprd.Option{
		daprd.WithAppID(p.workflow.Dapr().AppID()),
		daprd.WithResourceFiles(p.workflow.DB().GetComponent(t)),
		daprd.WithPlacementAddresses(p.workflow.Placement().Address()),
		daprd.WithSchedulerAddresses(p.workflow.Scheduler().Address()),
	}, append(fp, p.workflow.JoinOptions(t)...)...)...)

	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *publishcut) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	var executions atomic.Int64
	release := make(chan struct{})
	gate := make(chan struct{})
	gateReached := make(chan struct{}, 1)
	var gated atomic.Bool
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	wfFn := func(c *task.WorkflowContext) (any, error) {
		slow := c.CallActivity("Slow")
		if err := c.WaitForSingleEvent("go", time.Minute).Await(nil); err != nil {
			return nil, err
		}
		// Parks with the turn lock held so the publish queues behind it.
		if gated.CompareAndSwap(false, true) {
			gateReached <- struct{}{}
			<-gate
		}
		var out string
		if err := slow.Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}
	actFn := func(c task.ActivityContext) (any, error) {
		executions.Add(1)
		select {
		case <-release:
			return "published", nil
		case <-c.Context().Done():
			return nil, c.Context().Err()
		}
	}
	require.NoError(t, p.workflow.Registry().AddWorkflowN("PublishCut", wfFn))
	require.NoError(t, p.workflow.Registry().AddActivityN("Slow", actFn))
	cl := p.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "PublishCut")
	require.NoError(t, err)
	require.Eventually(t, func() bool { return executions.Load() == 1 }, time.Second*20, time.Millisecond*10)

	require.NoError(t, cl.RaiseEvent(ctx, id, "go"))
	select {
	case <-gateReached:
	case <-time.After(time.Second * 20):
		require.Fail(t, "the workflow turn never reached the gate")
	}
	close(release)

	p.joiner.Run(t, ctx)
	t.Cleanup(func() { p.joiner.Cleanup(t) })
	p.joiner.WaitUntilRunning(t, ctx)
	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("PublishCut", wfFn))
	require.NoError(t, registry.AddActivityN("Slow", actFn))
	joinerClient := client.NewTaskHubGrpcClient(p.joiner.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, joinerClient.StartWorkItemListener(ctx, registry))

	// Drain timeout 2s, retry backoff up to 2s.
	assert.Never(t, func() bool {
		return executions.Load() > 1
	}, time.Second*6, time.Millisecond*10, "the published body must not run again")

	close(gate)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.JSONEq(t, `"published"`, meta.GetOutput().GetValue())
	assert.Equal(t, int64(1), executions.Load())
}
