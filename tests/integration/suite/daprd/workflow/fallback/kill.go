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
	suite.Register(new(kill))
}

// kill stages instances through both daprds of an app while neither has a
// worker, so both are fallback hosts and each owns a share of them. One of
// them is killed abruptly, with no shutdown to give the types up. Placement
// drops it, the survivor takes the instances it owned over, and once a
// worker connects to the survivor every instance completes with the event
// raised for it.
type kill struct {
	workflow *workflow.Workflow
}

func (k *kill) Setup(t *testing.T) []framework.Option {
	k.workflow = newPair(t, 2*time.Second, fallbackSettle)

	return []framework.Option{
		framework.WithProcesses(k.workflow),
	}
}

func (k *kill) Run(t *testing.T, ctx context.Context) {
	k.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, k.workflow.RegistryN(1).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		return ev, nil
	}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, k.workflow.HostsActorType(c, ctx, 0, k.workflow.WorkflowActorType(0)))
		assert.True(c, k.workflow.HostsActorType(c, ctx, 1, k.workflow.WorkflowActorType(1)))
	}, time.Second*10, time.Millisecond*50)

	mgmt := []*client.TaskHubGrpcClient{
		k.workflow.ManagementClientN(t, ctx, 0),
		k.workflow.ManagementClientN(t, ctx, 1),
	}

	const instances = 20
	ids := make([]api.InstanceID, 0, instances)
	for i := range instances {
		m := mgmt[i%2]
		octx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := m.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-kill-%d", i))))
		require.NoError(t, err)
		require.NoError(t, m.RaiseEvent(octx, id, "go", api.WithEventPayload(fmt.Sprintf("ev-%d", i))))
		cancel()
		ids = append(ids, id)
	}

	// The daprd to kill owns some of the instances.
	owned, hosted := k.workflow.DaprN(0).ActiveActorCount(t, ctx, k.workflow.WorkflowActorType(0))
	require.True(t, hosted)
	require.Positive(t, owned)

	k.workflow.DaprN(0).Kill(t)

	// The survivor serves every instance once placement dropped the killed
	// daprd, still pending.
	waitPending(t, ctx, mgmt[1], time.Second*20, ids...)

	k.workflow.ConnectWorkerN(t, ctx, 1, k.workflow.RegistryN(1))

	for i, id := range ids {
		wctx, cancel := context.WithTimeout(ctx, time.Second*20)
		meta := fworkflow.WaitForCompleted(t, wctx, mgmt[1], id)
		cancel()
		assert.Equal(t, fmt.Sprintf(`"ev-%d"`, i), meta.GetOutput().GetValue(), id)
	}
}
