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

package fallback

import (
	"context"
	"fmt"
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
	suite.Register(new(reconnectdrain))
}

// reconnectdrain has a worker on each of two daprds of an app and, under a
// load of starts and raises through both, disconnects the worker of daprd 0
// and connects it again at once, several times over. Another daprd has an
// executor, so daprd 0 drains the workflow actor types when its worker
// leaves, and the reconnect registers them again while the drain is still
// in progress, until the next placement round: the registration halts the
// draining actors first. Every call succeeds and every instance completes.
type reconnectdrain struct {
	workflow *workflow.Workflow
}

func (r *reconnectdrain) Setup(t *testing.T) []framework.Option {
	r.workflow = newPair(t, time.Second, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reconnectdrain) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	wf := func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}
	require.NoError(t, r.workflow.RegistryN(0).AddWorkflowN("wf", wf))
	require.NoError(t, r.workflow.RegistryN(1).AddWorkflowN("wf", wf))

	r.workflow.ConnectWorkerN(t, ctx, 1, r.workflow.RegistryN(1))
	r.workflow.WaitForConnectedWorkersN(t, ctx, 1, 1)
	worker := r.workflow.ConnectWorkerN(t, ctx, 0, r.workflow.RegistryN(0))
	r.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

	mgmt := []*client.TaskHubGrpcClient{
		r.workflow.ManagementClientN(t, ctx, 0),
		r.workflow.ManagementClientN(t, ctx, 1),
	}

	load := workflow.StartConcurrently(t, 4, func(g, n int) (api.InstanceID, bool) {
		m := mgmt[g%2]
		// A start time skips the executor's wait for the instance to
		// start, as the worker may be gone before the instance starts.
		octx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer cancel()
		id := api.InstanceID(fmt.Sprintf("fallback-reconnectdrain-%d-%d", g, n))
		_, err := m.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(id), api.WithStartTime(time.Now()))
		if !assert.NoError(t, err, "start %s", id) {
			return "", false
		}
		assert.NoError(t, m.RaiseEvent(octx, id, "go"), "raise %s", id)
		return id, true
	})

	const rounds = 5
	for range rounds {
		load.WaitStarted(t, 1)
		worker.Disconnect(t)
		r.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
		worker = r.workflow.ConnectWorkerN(t, ctx, 0, r.workflow.RegistryN(0))
		r.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	}
	load.WaitStarted(t, 1)
	ids := load.Stop()
	require.NotEmpty(t, ids)

	// Both daprds host the types with their workers.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.workflow.HostsActorType(c, ctx, 0, r.workflow.ExecutorActorType(0)))
		assert.True(c, r.workflow.HostsActorType(c, ctx, 0, r.workflow.WorkflowActorType(0)))
	}, time.Second*10, time.Millisecond*10)

	fworkflow.WaitForAllCompleted(t, ctx, mgmt[0], ids...)
}
