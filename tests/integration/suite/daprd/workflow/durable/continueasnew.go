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
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(continueasnew))
}

// continueasnew answers a workflow turn with continue-as-new, which daprd
// runs again within the same turn. The intermediate result is answered at
// once; the final one only once the turn is saved.
type continueasnew struct {
	workflow *workflow.Workflow
}

const continueAsNewHold = 3 * time.Second

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithClusteredDeployment(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_TURN_SAVE_HOLD", continueAsNewHold.String()))),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)
	worker := c.workflow.ConnectRawWorker(t, ctx)
	cl := c.workflow.ManagementClient(t, ctx)
	id := workflow.StartWorkflow(t, ctx, cl, "durable-continueasnew")

	wi := worker.NextWorkflowItem(t)
	start := time.Now()
	_, err := worker.Client().CompleteWorkflowTask(ctx, &protos.WorkflowResponse{
		InstanceId:      wi.GetWorkflowRequest().GetInstanceId(),
		CompletionToken: wi.GetCompletionToken(),
		Actions: []*protos.WorkflowAction{{
			Id: 0,
			WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
				CompleteWorkflow: &protos.CompleteWorkflowAction{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_CONTINUED_AS_NEW,
					Result:         wrapperspb.String(`"again"`),
				},
			},
		}},
	})
	require.NoError(t, err)
	assert.Less(t, time.Since(start), continueAsNewHold, "the intermediate result must not wait for the save")

	resp := workflow.CompletedResponse(worker.NextWorkflowItem(t))
	var header metadata.MD
	_, err = worker.Client().CompleteWorkflowTask(ctx, resp, grpc.Header(&header))
	require.NoError(t, err)
	assert.Equal(t, []string{"true"}, header.Get(todo.DurableHeader))

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(),
		"the turn must be saved when the final CompleteWorkflowTask returns")
}
