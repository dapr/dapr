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

package durable

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(clustered))
}

// clustered runs two daprds under clustered deployment and sends a worker's
// CompleteWorkflowTask call to the daprd that does not own the workflow. The
// call must still be answered only once the owner saved the turn.
type clustered struct {
	workflow *workflow.Workflow
}

func (c *clustered) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.NewClustered(t, 2,
		daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_TURN_SAVE_HOLD", "3s")),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *clustered) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)
	workers := []*workflow.RawWorker{
		c.workflow.ConnectRawWorkerN(t, ctx, 0),
		c.workflow.ConnectRawWorkerN(t, ctx, 1),
	}
	cl := c.workflow.ManagementClient(t, ctx)
	id := workflow.StartWorkflow(t, ctx, cl, "durable-clustered")

	// The work item arrives on the stream of the daprd that owns the
	// workflow; the result goes to the other one.
	var (
		resp     *protos.WorkflowResponse
		nonOwner *workflow.RawWorker
	)
	select {
	case wi := <-workers[0].Items():
		resp, nonOwner = workflow.CompletedResponse(wi), workers[1]
	case wi := <-workers[1].Items():
		resp, nonOwner = workflow.CompletedResponse(wi), workers[0]
	case <-time.After(30 * time.Second):
		require.FailNow(t, "timed out waiting for a workflow work item")
	}
	require.NotEmpty(t, resp.GetInstanceId())

	var header metadata.MD
	_, err := nonOwner.Client().CompleteWorkflowTask(ctx, resp, grpc.Header(&header))
	require.NoError(t, err)
	assert.Equal(t, []string{"true"}, header.Get(todo.DurableHeader))

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"the turn must be saved when CompleteWorkflowTask returns")
}
