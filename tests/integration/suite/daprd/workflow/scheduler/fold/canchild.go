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

package fold

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	procscheduler "github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(canchild))
}

// canchild pins child completions against folding around ContinueAsNew
// under the fast path: each generation derives the same deterministic child
// instance ID, and folding the child's lock-holding completion publish can
// deadlock against the CAN successor dispatching back into the same child.
type canchild struct {
	daprd     *daprd.Daprd
	place     *placement.Placement
	scheduler *procscheduler.Scheduler
}

func (c *canchild) Setup(t *testing.T) []framework.Option {
	c.place = placement.New(t)
	c.scheduler = procscheduler.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	c.daprd = daprd.New(t,
		daprd.WithResourceFiles(db.GetComponent(t)),
		daprd.WithPlacementAddresses(c.place.Address()),
		daprd.WithSchedulerAddresses(c.scheduler.Address()),
		daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_JANITOR_PERIOD", "2s")),
	)

	return []framework.Option{
		framework.WithProcesses(c.place, c.scheduler, db, c.daprd),
	}
}

func (c *canchild) Run(t *testing.T, ctx context.Context) {
	c.scheduler.WaitUntilRunning(t, ctx)
	c.place.WaitUntilRunning(t, ctx)
	c.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()

	var childCalls atomic.Int32
	require.NoError(t, registry.AddWorkflowN("agent", func(octx *task.WorkflowContext) (any, error) {
		var input int
		if err := octx.GetInput(&input); err != nil {
			return nil, err
		}
		childCalls.Add(1)
		return input * 10, nil
	}))

	// No explicit child instance ID: each generation derives the child's
	// instance ID from the parent's instance ID and the task counter, which
	// ContinueAsNew resets, so every generation targets the same child
	// actor.
	require.NoError(t, registry.AddWorkflowN("loop", func(octx *task.WorkflowContext) (any, error) {
		var inc int
		if err := octx.GetInput(&inc); err != nil {
			return nil, err
		}

		var childResult int
		if err := octx.CallChildWorkflow("agent",
			task.WithChildWorkflowInput(inc)).Await(&childResult); err != nil {
			return nil, err
		}

		if inc < 2 {
			octx.ContinueAsNew(inc + 1)
			return nil, nil
		}
		return childResult, nil
	}))

	cl := client.NewTaskHubGrpcClient(c.daprd.GRPCConn(t, ctx), backend.DefaultLogger())
	require.NoError(t, cl.StartWorkItemListener(ctx, registry))

	id, err := cl.ScheduleNewWorkflow(ctx, "loop",
		api.WithInstanceID("fold-canchild-parent"),
		api.WithInput(0),
	)
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	meta, err := cl.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err,
		"parent did not complete: a folded child completion deadlocked against the CAN successor's child dispatch")
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED.String(), meta.GetRuntimeStatus().String(),
		"failure details: %v", meta.GetFailureDetails())
	require.Equal(t, `20`, meta.GetOutput().GetValue())
	require.Equal(t, int32(3), childCalls.Load())
}
