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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(reboot))
}

// reboot stops and restarts the only daprd of an app while it is a fallback
// host holding staged instances: started workflows and the events raised for
// them. The staged operations live in the actor state store and the
// scheduler, not in the daprd, so the restarted daprd, which is a fallback
// host again, serves them, and once a worker connects every instance
// completes with the event raised for it.
type reboot struct {
	workflow *workflow.Workflow
}

func (r *reboot) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *reboot) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		return ev, nil
	}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.workflow.HostsActorType(c, ctx, 0, r.workflow.WorkflowActorType(0)))
	}, time.Second*10, time.Millisecond*50)

	mgmt := r.workflow.ManagementClient(t, ctx)

	const instances = 10
	ids := make([]api.InstanceID, 0, instances)
	for i := range instances {
		octx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-reboot-%d", i))))
		require.NoError(t, err)
		require.NoError(t, mgmt.RaiseEvent(octx, id, "go", api.WithEventPayload(fmt.Sprintf("ev-%d", i))))
		cancel()
		ids = append(ids, id)
	}

	r.workflow.Dapr().RestartGraceful(t, ctx)
	r.workflow.Dapr().WaitUntilRunning(t, ctx)

	// The restarted daprd is a fallback host again and serves the staged
	// instances, still pending.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, r.workflow.HostsActorType(c, ctx, 0, r.workflow.WorkflowActorType(0)))
	}, time.Second*10, time.Millisecond*50)
	mgmt = r.workflow.ManagementClient(t, ctx)
	waitPending(t, ctx, mgmt, time.Second*10, ids...)

	r.workflow.ConnectWorker(t, ctx, r.workflow.Registry())

	for i, id := range ids {
		wctx, cancel := context.WithTimeout(ctx, time.Second*20)
		meta := fworkflow.WaitForCompleted(t, wctx, mgmt, id)
		cancel()
		assert.Equal(t, fmt.Sprintf(`"ev-%d"`, i), meta.GetOutput().GetValue(), id)
	}
}
