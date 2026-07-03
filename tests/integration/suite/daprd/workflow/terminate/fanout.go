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

package terminate

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(fanout))
}

type fanout struct {
	workflow *workflow.Workflow
}

func (f *fanout) Setup(t *testing.T) []framework.Option {
	f.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *fanout) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const (
		numChildren        = 8
		activitiesPerChild = 6
		blockStep          = activitiesPerChild - 1
	)

	childID := func(i int) string {
		return fmt.Sprintf("fanout-root-child-%d", i)
	}

	holdCh := make(chan struct{})
	var blocked atomic.Int64
	t.Cleanup(func() {
		select {
		case <-holdCh:
		default:
			close(holdCh)
		}
	})

	r := f.workflow.Registry()

	require.NoError(t, r.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
		var step int
		if err := actx.GetInput(&step); err != nil {
			return nil, err
		}
		if step == blockStep {
			blocked.Add(1)
			<-holdCh
		}
		return nil, nil
	}))

	require.NoError(t, r.AddWorkflowN("child", func(wctx *task.WorkflowContext) (any, error) {
		for s := range activitiesPerChild {
			if err := wctx.CallActivity("step", task.WithActivityInput(s)).Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))

	require.NoError(t, r.AddWorkflowN("root", func(wctx *task.WorkflowContext) (any, error) {
		tasks := make([]task.Task, numChildren)
		for i := range numChildren {
			tasks[i] = wctx.CallChildWorkflow("child",
				task.WithChildWorkflowInstanceID(childID(i)),
			)
		}
		for _, tk := range tasks {
			if err := tk.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}))

	cl := f.workflow.BackendClient(t, ctx)

	id, err := cl.ScheduleNewWorkflow(ctx, "root", api.WithInstanceID("fanout-root"))
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(numChildren), blocked.Load())
	}, time.Second*60, time.Millisecond*10)

	termErr := make(chan error, 1)
	go func() { termErr <- cl.TerminateWorkflow(ctx, id) }()
	select {
	case err = <-termErr:
		require.NoError(t, err)
	case <-time.After(time.Second * 20):
		require.Fail(t, "TerminateWorkflow hung and never returned")
	}

	// Children stay blocked through the terminate so each must end up
	// TERMINATED, never COMPLETED. holdCh is closed in the t.Cleanup above.
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "root never reached a terminal state")
	require.Equal(t, api.RUNTIME_STATUS_TERMINATED.String(), meta.GetRuntimeStatus().String())

	for i := range numChildren {
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			cmeta, cerr := cl.FetchWorkflowMetadata(ctx, api.InstanceID(childID(i)))
			if !assert.NoError(c, cerr) {
				return
			}
			assert.Equal(c, api.RUNTIME_STATUS_TERMINATED.String(), cmeta.GetRuntimeStatus().String())
		}, time.Second*60, time.Millisecond*10)
	}

	gotMeta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_TERMINATED.String(), gotMeta.GetRuntimeStatus().String())

	termErr2 := make(chan error, 1)
	go func() { termErr2 <- cl.TerminateWorkflow(ctx, id) }()
	select {
	case err = <-termErr2:
		require.NoError(t, err)
	case <-time.After(time.Second * 20):
		require.Fail(t, "repeated TerminateWorkflow on a terminated workflow hung")
	}
}
