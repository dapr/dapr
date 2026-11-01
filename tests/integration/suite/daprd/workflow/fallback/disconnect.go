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
	suite.Register(new(disconnect))
}

// disconnect starts workflows and raises their events without pause while
// the last worker disconnects, several times over: the mirror of connect.
// The daprd switches in place from a host with a worker to a fallback host,
// so the workflow actor type is hosted throughout: every call succeeds, none
// failing for want of a host, and every instance completes once a worker
// connects again.
type disconnect struct {
	workflow *workflow.Workflow
}

func (d *disconnect) Setup(t *testing.T) []framework.Option {
	d.workflow = workflow.New(t,
		workflow.WithStagedOperations(),
		withSettle(t, 0),
	)

	return []framework.Option{
		framework.WithProcesses(d.workflow),
	}
}

func (d *disconnect) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, d.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return nil, ctx.WaitForSingleEvent("go", time.Hour).Await(nil)
	}))

	mgmt := d.workflow.ManagementClient(t, ctx)

	const rounds = 10
	ids := make([]api.InstanceID, 0, rounds)
	for round := range rounds {
		worker := d.workflow.ConnectWorker(t, ctx, d.workflow.Registry())
		d.workflow.WaitForConnectedWorkers(t, ctx, 1)

		load := workflow.StartConcurrently(t, 4, func(g, n int) (api.InstanceID, bool) {
			// A start time skips the executor's wait for the instance
			// to start, which a daprd with a worker makes, as the worker may
			// be gone before the instance starts.
			octx, cancel := context.WithTimeout(ctx, time.Second*10)
			defer cancel()
			id, err := mgmt.ScheduleNewWorkflow(octx, "wf",
				api.WithInstanceID(api.InstanceID(fmt.Sprintf("fallback-disconnect-%d-%d-%d", round, g, n))),
				api.WithStartTime(time.Now()),
			)
			if !assert.NoError(t, err, "start during the disconnect of round %d", round) {
				return "", false
			}
			assert.NoError(t, mgmt.RaiseEvent(octx, id, "go"), "raise during the disconnect of round %d", round)
			return id, true
		})

		// The load runs before the disconnect, and on after it against the
		// fallback host.
		load.WaitStarted(t, 1)
		worker.Disconnect(t)
		d.workflow.WaitForNoConnectedWorkers(t, ctx)
		load.WaitStarted(t, 1)
		ids = append(ids, load.Stop()...)
	}

	d.workflow.ConnectWorker(t, ctx, d.workflow.Registry())
	fworkflow.WaitForAllCompleted(t, ctx, mgmt, ids...)
}
