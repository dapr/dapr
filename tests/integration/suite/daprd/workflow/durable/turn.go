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
	suite.Register(new(turn))
}

// turn holds the save at the end of a workflow turn and requires the
// worker's CompleteWorkflowTask call to be answered only once the turn is
// saved.
type turn struct {
	workflow *workflow.Workflow
}

func (r *turn) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithClusteredDeployment(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_TURN_SAVE_HOLD", "3s"))),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *turn) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)
	worker := r.workflow.ConnectRawWorker(t, ctx)
	cl := r.workflow.ManagementClient(t, ctx)
	id := workflow.StartWorkflow(t, ctx, cl, "durable-turn")

	resp := workflow.CompletedResponse(worker.NextWorkflowItem(t))
	require.NotEmpty(t, resp.GetCompletionToken())

	var header metadata.MD
	_, err := worker.Client().CompleteWorkflowTask(ctx, resp, grpc.Header(&header))
	require.NoError(t, err)
	assert.Equal(t, []string{"true"}, header.Get(todo.DurableHeader))

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"the turn must be saved when CompleteWorkflowTask returns")
}
