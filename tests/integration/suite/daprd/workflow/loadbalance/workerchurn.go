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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workerchurn))
}

// workerchurn disconnects one daprd's worker twice while it holds activities in
// flight across a clustered deployment.
type workerchurn struct {
	workflow *workflow.Workflow
}

func (w *workerchurn) Setup(t *testing.T) []framework.Option {
	// The churned host is back within milliseconds, so a cancelled activity
	// re-lands on it rather than moving. Under the fast path that activity has
	// no durable reminder while its local drive is live, and the disconnect
	// cancels the drive, so the janitor is what re-dispatches it. Its default
	// 20s period does not fit the test budget.
	w.workflow = workflow.NewClustered(t, 3, daprd.WithWorkflowJanitorPeriod(t, 2*time.Second))

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workerchurn) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	const (
		daprds    = 3
		churned   = 2
		instances = 30
		steps     = 5
	)

	var held atomic.Int32
	for i := range daprds {
		reg := w.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("churn", func(ctx *task.WorkflowContext) (any, error) {
			for j := range steps {
				if err := ctx.CallActivity("step", task.WithActivityInput(j)).Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		}))
		if i == churned {
			require.NoError(t, reg.AddActivityN("step", func(actx task.ActivityContext) (any, error) {
				held.Add(1)
				<-actx.Context().Done()
				return nil, actx.Context().Err()
			}))
			continue
		}
		require.NoError(t, reg.AddActivityN("step", func(task.ActivityContext) (any, error) {
			return nil, nil
		}))
	}

	clients := make([]*client.TaskHubGrpcClient, churned)
	for i := range churned {
		clients[i] = w.workflow.BackendClientN(t, ctx, i)
	}
	worker := w.workflow.ConnectWorkerN(t, ctx, churned, w.workflow.RegistryN(churned))
	w.workflow.WaitForConnectedWorkersN(t, ctx, churned, 1)

	ids := make([]api.InstanceID, instances)
	for i := range instances {
		ids[i] = api.InstanceID(fmt.Sprintf("churn-%d", i))
		_, err := clients[i%churned].ScheduleNewWorkflow(ctx, "churn", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}

	allCompleted := func() bool {
		for _, id := range ids {
			meta, err := clients[0].FetchWorkflowMetadata(ctx, id)
			if err != nil || meta.GetRuntimeStatus() != protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED {
				return false
			}
		}
		return true
	}

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col, held.Load(), int32(3))
	}, 10*time.Second, 10*time.Millisecond)
	for range 2 {
		worker.Disconnect(t)
		w.workflow.WaitForNoConnectedWorkersN(t, ctx, churned)
		before := held.Load()
		worker = w.workflow.ConnectWorkerN(t, ctx, churned, w.workflow.RegistryN(churned))
		w.workflow.WaitForConnectedWorkersN(t, ctx, churned, 1)
		// The churned host must resume executing after the reconnect, unless
		// nothing is left for it to execute: an instance progresses only while
		// its current activity hashes elsewhere, so how many still have work on
		// this host is a lottery, and a fixed increment is not always
		// satisfiable.
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			assert.True(col, held.Load() > before || allCompleted(),
				"the churned host must resume executing, or every instance must have completed")
		}, 10*time.Second, 10*time.Millisecond)
	}
	worker.Disconnect(t)
	w.workflow.WaitForNoConnectedWorkersN(t, ctx, churned)

	// The instances still pinned to the churned host must be re-placed onto
	// the two live ones and complete.
	fworkflow.WaitForAllCompleted(t, ctx, clients[0], ids...)
}
