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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(singlestream))
}

// singlestream restarts the scheduler while two activities are in flight on
// the sidecar's only job stream, with the scheduler serving actor placement.
// The restarted scheduler becomes placement leader only once a sidecar
// connects, and the activities need a placement leader to report their
// results, so the sidecar must reconnect its job stream while they still
// run.
type singlestream struct {
	workflow *workflow.Workflow
}

func (s *singlestream) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithSchedulerPlacement(),
		workflow.WithDaprdOptions(0, daprd.WithSchedulerJobStreams(1)),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *singlestream) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const n = 2
	calls := make([]atomic.Int32, n)
	var returned atomic.Int32
	release := make(chan struct{})

	require.NoError(t, s.workflow.Registry().AddWorkflowN("wf",
		func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, n)
			for i := range tasks {
				tasks[i] = ctx.CallActivity(fmt.Sprintf("act-%d", i))
			}
			for _, ta := range tasks {
				if err := ta.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
	for i := range n {
		require.NoError(t, s.workflow.Registry().AddActivityN(fmt.Sprintf("act-%d", i),
			func(ctx task.ActivityContext) (any, error) {
				calls[i].Add(1)
				defer returned.Add(1)
				select {
				case <-release:
				case <-ctx.Context().Done():
				}
				return nil, nil
			}))
	}

	cl := s.workflow.BackendClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "wf")
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range n {
			assert.Equal(c, int32(1), calls[i].Load(), "activity %d", i)
		}
	}, 20*time.Second, 10*time.Millisecond)

	sched := s.workflow.Scheduler()
	sched.RestartGraceful(t, ctx)
	sched.WaitUntilRunning(t, ctx)
	sched.WaitUntilLeadership(t, ctx, 1)

	// The scenario needs both activities still in flight across the restart:
	// not returned early, not run again.
	require.Zero(t, returned.Load(), "an activity body returned before its release")
	for i := range n {
		require.Equal(t, int32(1), calls[i].Load(), "activity %d ran again across the restart", i)
	}

	close(release)

	cl = s.workflow.BackendClient(t, ctx)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "the workflow must complete: its activities report through a placement leader, which needs the sidecar's job stream to reconnect")
	require.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	for i := range n {
		assert.Equal(t, int32(1), calls[i].Load(), "activity %d", i)
	}
}
