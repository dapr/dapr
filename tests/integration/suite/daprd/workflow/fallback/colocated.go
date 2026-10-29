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
	suite.Register(new(colocated))
}

// colocated runs two daprds of one app, only one of which has a worker, as
// when a workflow app is scaled out next to replicas that do not run
// workflow code. The daprd without a worker hosts the workflow actor types
// only while no daprd of the app has a worker: once the other has had one
// for the damping period it gives them up, and owns no instance from then
// on, wherever the calls come from. Every instance runs on the daprd with
// the worker at once.
type colocated struct {
	workflow *workflow.Workflow
}

func (c *colocated) Setup(t *testing.T) []framework.Option {
	c.workflow = newPair(t, 2*time.Second, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *colocated) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, c.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var in string
		if err := ctx.GetInput(&in); err != nil {
			return nil, err
		}
		return in, nil
	}))

	c.workflow.ConnectWorkerN(t, ctx, 0, c.workflow.RegistryN(0))
	c.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

	// The daprd without a worker gives the types up after the damping period.
	require.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.False(col, c.workflow.HostsActorType(col, ctx, 1, c.workflow.WorkflowActorType(1)))
		assert.True(col, c.workflow.HostsActorType(col, ctx, 0, c.workflow.WorkflowActorType(0)))
	}, time.Second*20, time.Millisecond*50)

	mgmt := []*client.TaskHubGrpcClient{
		c.workflow.ManagementClientN(t, ctx, 0),
		c.workflow.ManagementClientN(t, ctx, 1),
	}

	const instances = 20
	ids := make([]api.InstanceID, 0, instances)
	for i := range instances {
		// Calls through either daprd: the one without a worker routes them.
		m := mgmt[i%2]
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := m.ScheduleNewWorkflow(sctx, "wf",
			api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-colocated-%d", i))),
			api.WithInput(fmt.Sprintf("in-%d", i)),
		)
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)
	}

	for i, id := range ids {
		wctx, cancel := context.WithTimeout(ctx, time.Second*10)
		meta := fworkflow.WaitForCompleted(t, wctx, mgmt[(i+1)%2], id)
		cancel()
		assert.Equal(t, fmt.Sprintf(`"in-%d"`, i), meta.GetOutput().GetValue(), id)
	}

	// No instance was ever owned by the daprd without a worker: it hosts no
	// workflow actor and served nothing as a fallback host since.
	assert.False(t, c.workflow.HostsActorType(t, ctx, 1, c.workflow.WorkflowActorType(1)))
	assert.True(t, c.workflow.HostsActorType(t, ctx, 0, c.workflow.WorkflowActorType(0)))
	assert.Zero(t, stagedStarts(t, ctx, c.workflow, 1))
}
