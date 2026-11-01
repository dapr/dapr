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

package loadbalance

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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(strandedactivity))
}

// strandedactivity pins that an activity executing on a daprd whose worker
// disconnects for good is re-placed onto a daprd that has one, and the
// workflow completes. The topology is fixed rather than left to the actor
// hash: only one host has a worker when the instance starts, so every actor
// of the instance lands there, and the replacement host joins only after it
// has gone. workerchurn reaches the same situation through a hash lottery,
// and on its failing path never got as far as checking it.
type strandedactivity struct {
	workflow *workflow.Workflow
}

func (s *strandedactivity) Setup(t *testing.T) []framework.Option {
	// Under the fast path the disconnect can cancel the local drive while the
	// execution claim is still live, and that path skips the durable-reminder
	// escalation (activity/drive.go), leaving the orchestrator janitor as the
	// only re-driver. Its default 20s period exceeds the bound below.
	s.workflow = workflow.NewClustered(t, 2, daprd.WithWorkflowJanitorPeriod(t, 2*time.Second))
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *strandedactivity) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const (
		alive    = 0
		stranded = 1
	)

	var blocked, ranAlive atomic.Int32
	for i := range 2 {
		reg := s.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("stranded", func(ctx *task.WorkflowContext) (any, error) {
			var out string
			if err := ctx.CallActivity("step").Await(&out); err != nil {
				return nil, err
			}
			return out, nil
		}))
		if i == stranded {
			require.NoError(t, reg.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
				blocked.Add(1)
				<-actx.Context().Done()
				return nil, actx.Context().Err()
			}))
			continue
		}
		require.NoError(t, reg.AddActivityN("step", func(task.ActivityContext) (any, error) {
			ranAlive.Add(1)
			return "alive", nil
		}))
	}

	// Only the stranded host has a worker, so it alone registers the workflow
	// actor types and the instance's actors all land on it.
	strandedWorker := s.workflow.ConnectWorkerN(t, ctx, stranded, s.workflow.RegistryN(stranded))
	s.workflow.WaitForConnectedWorkersN(t, ctx, stranded, 1)

	mgmt := s.workflow.ManagementClientN(t, ctx, alive)
	id, err := mgmt.ScheduleNewWorkflow(ctx, "stranded", api.WithInstanceID("stranded-1"))
	require.NoError(t, err)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int32(1), blocked.Load())
	}, 10*time.Second, 10*time.Millisecond)

	version := s.workflow.PlacementVersion(t, ctx)
	strandedWorker.Disconnect(t)
	s.workflow.WaitForNoConnectedWorkersN(t, ctx, stranded)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, hosted := s.workflow.DaprN(stranded).ActiveActorCount(c, ctx, s.workflow.ActivityActorType(stranded))
		assert.False(c, hosted, "a host without a worker must stop advertising the activity actor type")
	}, 10*time.Second, 10*time.Millisecond)

	s.workflow.ConnectWorkerN(t, ctx, alive, s.workflow.RegistryN(alive))
	s.workflow.WaitForConnectedWorkersN(t, ctx, alive, 1)

	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	t.Cleanup(cancel)
	meta, err := mgmt.WaitForWorkflowCompletion(wctx, id)
	require.NoError(t, err, "the stranded activity must be re-placed onto the live host")
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"alive"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(1), ranAlive.Load(), "the live host must execute the stranded activity exactly once")
	assert.Equal(t, int32(1), blocked.Load(), "the stranded host must not have executed it again")
	assert.Greater(t, s.workflow.PlacementVersion(t, ctx), version, "the placement authority must disseminate the withdrawal")
}
