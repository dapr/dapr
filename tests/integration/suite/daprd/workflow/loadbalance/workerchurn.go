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
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
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
	w.workflow = workflow.NewClustered(t, 3)

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

	var threshold int32 = 3
	for range 2 {
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			assert.GreaterOrEqual(col, held.Load(), threshold)
		}, time.Minute, 10*time.Millisecond)
		worker.Disconnect(t)
		w.workflow.WaitForNoConnectedWorkersN(t, ctx, churned)
		worker = w.workflow.ConnectWorkerN(t, ctx, churned, w.workflow.RegistryN(churned))
		w.workflow.WaitForConnectedWorkersN(t, ctx, churned, 1)
		threshold = held.Load() + 3
	}
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col, held.Load(), threshold)
	}, time.Minute, 10*time.Millisecond)
	worker.Disconnect(t)
	w.workflow.WaitForNoConnectedWorkersN(t, ctx, churned)

	fworkflow.WaitForAllCompleted(t, ctx, clients[0], ids...)
}
