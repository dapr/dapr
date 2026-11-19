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
	suite.Register(new(schedulerplacement))
}

// schedulerplacement checks workflow turns are pulled when the scheduler also
// serves actor placement, and workflows progress while one slot is held.
type schedulerplacement struct {
	workflow *workflow.Workflow
}

const schedulerplacementAppID = "workflow-schedplacement"

func (s *schedulerplacement) Setup(t *testing.T) []framework.Option {
	// Two slots, as maxConcurrentWorkflowInvocations also caps the turns a
	// replica runs as placement owner, so the held turn leaves its owner room.
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowschedplacement
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 2
    maxConcurrentActivityInvocations: 2
    dispatchMode: pull
`
	s.workflow = workflow.New(t,
		workflow.WithSchedulerPlacement(),
		workflow.WithDaprds(3),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(schedulerplacementAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(schedulerplacementAppID)),
		workflow.WithDaprdOptions(2, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(schedulerplacementAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *schedulerplacement) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	held := make(chan struct{})
	var heldOnce sync.Once
	release := make(chan struct{})
	done := ctx.Done()
	for i := range 3 {
		registry := s.workflow.RegistryN(i)
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
		require.NoError(t, registry.AddWorkflowN("steps", func(ctx *task.WorkflowContext) (any, error) {
			for range 3 {
				if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
	}

	client := s.workflow.BackendClientN(t, ctx, 0)
	s.workflow.BackendClientN(t, ctx, 1)
	s.workflow.BackendClientN(t, ctx, 2)

	hold, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	select {
	case <-held:
	case <-time.After(time.Second * 10):
		require.Fail(t, "hold turn not held")
	}
	s.workflow.Scheduler().WaitUntilPullInflight(t, ctx, 1, "app_id:"+schedulerplacementAppID)

	// The other slots keep serving turns while hold has one.
	ids := make([]api.InstanceID, 6)
	for i := range ids {
		ids[i], err = client.ScheduleNewWorkflow(ctx, "steps")
		require.NoError(t, err)
	}
	waitCtx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(waitCtx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}

	close(release)
	meta, err := client.WaitForWorkflowCompletion(ctx, hold)
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
