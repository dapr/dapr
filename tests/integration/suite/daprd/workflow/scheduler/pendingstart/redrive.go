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

package pendingstart

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
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
	suite.Register(new(redrive))
}

// redrive covers the recovery of a workflow whose creation was
// committed but whose start reminder was never registered and whose host died
// before it could be armed: committed state, empty history, no Scheduler job,
// PENDING forever. The client never re-issues the create; it only reads the
// status. Both status read paths (the direct store read behind
// GetWorkflow, and the actor stream behind WaitForWorkflowStart) must notice
// the overdue pending start and re-drive it.
type redrive struct {
	workflow *workflow.Workflow
}

func (p *redrive) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "1s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *redrive) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.Registry().AddWorkflowN("PendingStart", func(c *task.WorkflowContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		return "Hello, " + input + "!", nil
	}))
	client := p.workflow.BackendClient(t, ctx)
	gclient := p.workflow.GRPCClient(t, ctx)

	strand := func(instanceID string) {
		p.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 1, nil, []*protos.HistoryEvent{{
			EventId:   -1,
			Timestamp: timestamppb.New(time.Now().Add(-time.Minute)),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name:             "PendingStart",
					Input:            wrapperspb.String(`"Dapr"`),
					WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
				},
			},
		}})
		require.Zero(t, p.workflow.Scheduler().JobKeyCount(t, ctx, "||"+instanceID+"||start-es-"),
			"precondition: no start reminder is registered for the stranded instance")
	}

	t.Run("status read", func(t *testing.T) {
		const instanceID = "pending-start-redrive-status"
		strand(instanceID)

		resp, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: instanceID, WorkflowComponent: "dapr"})
		require.NoError(t, err)
		require.Equal(t, "PENDING", resp.GetRuntimeStatus(), "precondition: the fabricated shape reads as PENDING")

		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: instanceID, WorkflowComponent: "dapr"})
			if assert.NoError(c, gerr) {
				assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
			}
		}, 30*time.Second, 50*time.Millisecond, "a status read of an overdue pending start must re-drive it")
		assert.Positive(t, p.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:pending_start_redriven"))
	})

	t.Run("status wait", func(t *testing.T) {
		const instanceID = "pending-start-redrive-wait"
		strand(instanceID)

		meta, err := client.WaitForWorkflowStart(ctx, api.InstanceID(instanceID))
		require.NoError(t, err, "a status wait on an overdue pending start must re-drive it")
		require.NotEqual(t, api.RUNTIME_STATUS_PENDING, meta.GetRuntimeStatus())

		meta, err = client.WaitForWorkflowCompletion(ctx, api.InstanceID(instanceID))
		require.NoError(t, err)
		assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
		assert.Equal(t, `"Hello, Dapr!"`, meta.GetOutput().GetValue())
		assert.GreaterOrEqual(t, p.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:pending_start_redriven"), float64(2),
			"each stranded instance is re-driven exactly once per grace")
	})
}
