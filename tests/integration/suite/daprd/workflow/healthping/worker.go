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

package healthping

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(worker))
}

// worker verifies that an SDK worker advertises the health ping capability,
// receives pings while idle without reconnecting, and still runs workflows on
// the same stream afterwards.
type worker struct {
	workflow *workflow.Workflow
}

func (w *worker) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithWorkflowHealthPingInterval(t, pingInterval)),
		workflow.WithAddOrchestrator(t, "healthping", func(*task.WorkflowContext) (any, error) {
			return "done", nil
		}),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *worker) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	cw := w.workflow.ConnectWorker(t, ctx, w.workflow.Registry())
	w.workflow.WaitForConnectedWorkers(t, ctx, 1)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, cw.Observer.HealthPings(), 3)
	}, time.Second*5, time.Millisecond*10)

	cl := w.workflow.ManagementClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "healthping")
	require.NoError(t, err)
	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
	assert.Equal(t, `"done"`, meta.GetOutput().GetValue())

	assert.Equal(t, 1, cw.Observer.WorkItemStreams(), "health pings must not make the worker reconnect")
}
