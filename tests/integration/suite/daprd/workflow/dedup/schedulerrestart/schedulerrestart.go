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

package schedulerrestart

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(schedulerrestart))
}

type schedulerrestart struct {
	workflow *workflow.Workflow
}

func (s *schedulerrestart) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *schedulerrestart) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var activityCalls atomic.Int32
	holdCh := make(chan struct{})

	require.NoError(t, s.workflow.Registry().AddWorkflowN("wf",
		func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("act").Await(nil)
		}))
	require.NoError(t, s.workflow.Registry().AddActivityN("act",
		func(ctx task.ActivityContext) (any, error) {
			activityCalls.Add(1)
			select {
			case <-holdCh:
			case <-ctx.Context().Done():
			}
			return nil, nil
		}))

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), activityCalls.Load())
	}, 20*time.Second, 10*time.Millisecond)

	sched := s.workflow.Scheduler()
	sched.RestartGraceful(t, ctx)
	sched.WaitUntilRunning(t, ctx)
	sched.WaitUntilLeadership(t, ctx, 1)

	// After the restart, the scheduler will hammer the activity reminder until
	// it gets a SUCCESS ack. Confirm that the SDK callback is not invoked a
	// second time during this storm.
	require.Never(t, func() bool {
		return activityCalls.Load() > 1
	}, 5*time.Second, 100*time.Millisecond,
		"activity must not be re-executed while its inflight call is still pending")

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_RUNNING", meta.GetRuntimeStatus().String(),
		"workflow must still be running after scheduler restart")

	// Release the held activity so the workflow can complete.
	close(holdCh)

	cl = s.workflow.BackendClient(t, ctx)
	meta, err = cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, int32(1), activityCalls.Load(),
		"activity must execute exactly once even when scheduler retries the reminder")
}
