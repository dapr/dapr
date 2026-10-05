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

package reconnect

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(unregisterclosing))
}

// unregisterclosingCycles is how many times the test disconnects the last
// worker of daprd 0. The unregister used to fail at random for a closing
// stream context, so each cycle is a separate chance to catch it.
const unregisterclosingCycles = 10

// unregisterclosing disconnects the last worker of a daprd while the stream's
// transport closes during the disconnect callback. The daprd must stop hosting
// the workflow actor types each time. Otherwise placement keeps sending
// workflows to a daprd that has no worker to run them.
type unregisterclosing struct {
	workflow *workflow.Workflow
}

func (u *unregisterclosing) Setup(t *testing.T) []framework.Option {
	uid, err := uuid.NewRandom()
	require.NoError(t, err)

	// Both daprds are replicas of one app, so they host the same workflow
	// actor types.
	u.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(uid.String()),
			daprd.WithExecOptions(exec.WithEnvVars(t,
				"DAPR_WORKFLOW_TEST_CLOSE_DISCONNECT_CONTEXT", strconv.Itoa(unregisterclosingCycles),
			)),
		),
		workflow.WithDaprdOptions(1, daprd.WithAppID(uid.String())),
	)

	return []framework.Option{
		framework.WithProcesses(u.workflow),
	}
}

func (u *unregisterclosing) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	for i := range 2 {
		reg := u.workflow.RegistryN(i)
		require.NoError(t, reg.AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
			return nil, ctx.CallActivity("act").Await(nil)
		}))
		require.NoError(t, reg.AddActivityN("act", func(task.ActivityContext) (any, error) {
			return nil, nil
		}))
	}

	// daprd 1 keeps a worker for the whole test, so the workflow actor types
	// stay registered with placement while daprd 0's worker comes and goes.
	u.workflow.ConnectWorkerN(t, ctx, 1, u.workflow.RegistryN(1))
	u.workflow.WaitForConnectedWorkersN(t, ctx, 1, 1)

	for i := range unregisterclosingCycles {
		worker := u.workflow.ConnectWorkerN(t, ctx, 0, u.workflow.RegistryN(0))
		u.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

		worker.Disconnect(t)
		u.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
		require.EventuallyWithT(t, func(c *assert.CollectT) {
			md := u.workflow.DaprN(0).GetMetadata(c, ctx)
			if !assert.NotNil(c, md) || !assert.NotNil(c, md.ActorRuntime) {
				return
			}
			assert.Empty(c, md.ActorRuntime.ActiveActors)
		}, time.Second*10, time.Millisecond*10,
			"cycle %d: daprd 0 still hosts the workflow actor types after its last worker disconnected", i)
	}

	// With the types gone from daprd 0, placement sends every instance to
	// daprd 1, whose worker runs it.
	cl := u.workflow.ManagementClientN(t, ctx, 1)
	ids := make([]api.InstanceID, 10)
	for i := range ids {
		ids[i] = api.InstanceID(fmt.Sprintf("unregisterclosing-%d", i))
		_, err := cl.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID(ids[i]))
		require.NoError(t, err)
	}
	fworkflow.WaitForAllCompleted(t, ctx, cl, ids...)
}
