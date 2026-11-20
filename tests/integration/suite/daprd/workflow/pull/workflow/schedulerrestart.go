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
	"sync"
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
	suite.Register(new(schedulerrestart))
}

// schedulerrestart checks parked workflow turns survive a scheduler restart
// and run once the workflow slot frees.
type schedulerrestart struct {
	workflow *workflow.Workflow
}

const schedulerrestartAppID = "workflow-schedrestart"

func (s *schedulerrestart) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithAppID(schedulerrestartAppID), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowschedrestart
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *schedulerrestart) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	held := make(chan struct{})
	var heldOnce sync.Once
	release := make(chan struct{})
	done := ctx.Done()
	var ran atomic.Int64
	registry := s.workflow.Registry()
	require.NoError(t, registry.AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		// The start returns once the first turn commits, so hold blocks in its second.
		if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			// A redelivered turn runs this again.
			heldOnce.Do(func() { close(held) })
			select {
			case <-release:
			case <-done:
			}
		}
		return nil, nil
	}))
	require.NoError(t, registry.AddWorkflowN("plain", func(ctx *task.WorkflowContext) (any, error) {
		if !ctx.IsReplaying {
			ran.Add(1)
		}
		return nil, nil
	}))

	client := s.workflow.BackendClient(t, ctx)
	hold, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	select {
	case <-held:
	case <-time.After(time.Second * 10):
		require.Fail(t, "hold turn not held")
	}

	backlog := func(want float64) {
		t.Helper()
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			assert.InDelta(col, want, s.workflow.Scheduler().Metrics(col, ctx).SumWithLabels(
				"dapr_scheduler_workflow_backlog", "app_id:"+schedulerrestartAppID), 0)
		}, time.Second*10, time.Millisecond*10)
	}

	// These first turns wait in the scheduler while hold has the slot.
	ids := []api.InstanceID{"plain-0", "plain-1"}
	errCh := make(chan error, len(ids))
	for _, id := range ids {
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, "plain",
				api.WithInstanceID(id))
			errCh <- err
		}()
	}
	backlog(2)

	s.workflow.Scheduler().RestartGraceful(t, ctx)
	s.workflow.Scheduler().WaitUntilRunning(t, ctx)

	// The parked turns are jobs the scheduler keeps, so they park again after
	// the restart behind the held slot.
	backlog(2)
	assert.Equal(t, int64(0), ran.Load())

	close(release)
	for range ids {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 10):
			require.Fail(t, "plain did not start")
		}
	}
	for _, id := range append(ids, hold) {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
	}
	assert.Equal(t, int64(2), ran.Load())
	backlog(0)
}
