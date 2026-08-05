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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(canorphan))
}

// canorphan verifies that a workflow which continues-as-new while an
// unawaited activity is still executing completes with the new generation's
// result. The orphaned activity and the new generation's first activity share
// a task ID and therefore an activity actor, so the new generation's
// execution must wait for the orphan to finish and the orphan's stale result
// must be discarded.
type canorphan struct {
	workflow *workflow.Workflow
}

func (c *canorphan) Setup(t *testing.T) []framework.Option {
	c.workflow = newClusteredDeployment(t, 2)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *canorphan) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	orphanStarted := make(chan struct{})
	releaseOrphan := make(chan struct{})

	require.NoError(t, c.workflow.RegistryN(0).AddWorkflowN("canorphan", func(ctx *task.WorkflowContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == "first" {
			// Schedule but never await; this activity is orphaned by the
			// ContinueAsNew below while it is still executing.
			ctx.CallActivity("gated", task.WithActivityInput("first"))
			if err := ctx.WaitForSingleEvent("proceed", time.Minute).Await(nil); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew("second")
			return nil, nil
		}

		var out string
		if err := ctx.CallActivity("gated", task.WithActivityInput("second")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, c.workflow.RegistryN(0).AddActivityN("gated", func(ctx task.ActivityContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input == "first" {
			close(orphanStarted)
			<-releaseOrphan
			return "done-first", nil
		}
		return "done-second", nil
	}))
	_ = c.workflow.BackendClientN(t, ctx, 0)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(c.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		c.workflow.DaprN(0).GRPCConn(t, ctx),
		c.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	id, err := client.ScheduleNewWorkflow(ctx, "canorphan", api.WithInput("first"))
	require.NoError(t, err)

	select {
	case <-orphanStarted:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the orphaned activity to start")
	}

	// Continue-as-new while the orphan is mid-execution. The new generation
	// schedules the same task ID, colliding on the same activity actor.
	require.NoError(t, client.RaiseEvent(ctx, id, "proceed"))

	time.Sleep(time.Second * 2)
	close(releaseOrphan)

	metadata, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, `"done-second"`, metadata.GetOutput().GetValue())
}
