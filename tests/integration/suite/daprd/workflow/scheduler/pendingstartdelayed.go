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

package scheduler

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
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(pendingstartdelayed))
}

// pendingstartdelayed: the status-read re-drive of a pending start measures
// overdue from the scheduled start, not the creation time. A delayed start
// whose reminder was lost must stay PENDING until its scheduled time plus the
// grace, and be re-driven after it.
type pendingstartdelayed struct {
	workflow *workflow.Workflow
}

func (p *pendingstartdelayed) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "1s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *pendingstartdelayed) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.Registry().AddWorkflowN("Delayed", func(*task.WorkflowContext) (any, error) {
		return "ran", nil
	}))
	p.workflow.BackendClient(t, ctx)
	gclient := p.workflow.GRPCClient(t, ctx)

	const instanceID = "pending-start-delayed"
	scheduled := time.Now().Add(4 * time.Second)
	p.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 1, nil, []*protos.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.New(time.Now().Add(-time.Minute)),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:                    "Delayed",
				Input:                   wrapperspb.String(`"Dapr"`),
				WorkflowInstance:        &protos.WorkflowInstance{InstanceId: instanceID},
				ScheduledStartTimestamp: timestamppb.New(scheduled),
			},
		},
	}})

	redriven := func() float64 {
		return p.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:pending_start_redriven")
	}

	// Before the scheduled start nothing is overdue, however old the create.
	for time.Now().Before(scheduled.Add(-500 * time.Millisecond)) {
		resp, err := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: instanceID, WorkflowComponent: "dapr"})
		require.NoError(t, err)
		require.Equal(t, "PENDING", resp.GetRuntimeStatus())
		require.Zero(t, redriven(), "a delayed start must not be re-driven before its scheduled time")
		time.Sleep(100 * time.Millisecond)
	}
	require.Zero(t, p.workflow.Scheduler().JobKeyCount(t, ctx, "||"+instanceID+"||start-es-"))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: instanceID, WorkflowComponent: "dapr"})
		if assert.NoError(c, gerr) {
			assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 50*time.Millisecond, "once the scheduled start is overdue by the grace the status read must re-drive it")
	assert.Positive(t, redriven())
}
