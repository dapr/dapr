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

package held

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(expired))
}

// expired holds a start for longer than the hold timeout. When a worker
// connects, the start does not run: the instance is recorded as failed, so a
// client that was told the start was accepted can see what became of it.
type expired struct {
	workflow *workflow.Workflow
}

func (e *expired) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t,
		workflow.WithHeldOperations(),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(
			exec.WithEnvVars(t, "DAPR_WORKFLOW_HELD_OPERATION_TIMEOUT", "1s"),
		)),
	)

	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *expired) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	var ran atomic.Bool
	require.NoError(t, e.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		ran.Store(true)
		return nil, nil
	}))

	mgmt := e.workflow.ManagementClient(t, ctx)
	id, err := mgmt.ScheduleNewWorkflow(ctx, "wf", api.WithInstanceID("held-expired"))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Len(c, heldKeys(t, ctx, e.workflow, id), 1)
	}, time.Second*10, time.Millisecond*10)

	// Outlive the 1s hold timeout before any worker connects.
	time.Sleep(time.Second * 2)

	e.workflow.ConnectWorker(t, ctx, e.workflow.Registry())

	meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, meta.GetRuntimeStatus())
	assert.Equal(t, "DAPR_WORKFLOW_HELD_START_EXPIRED", meta.GetFailureDetails().GetErrorType())
	assert.Equal(t, "wf", meta.GetName())
	assert.False(t, ran.Load(), "an expired held start must not run")
}
