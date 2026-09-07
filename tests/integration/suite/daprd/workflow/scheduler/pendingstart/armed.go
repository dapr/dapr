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
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(armed))
}

// armed: the status-read re-drive of an overdue pending start
// must leave a start reminder that is registered alone. Overwriting it would
// reset its due time and fire it early; only a missing reminder is
// re-asserted.
type armed struct {
	workflow *workflow.Workflow
}

func (p *armed) Setup(t *testing.T) []framework.Option {
	p.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_PENDING_START_REDRIVE_GRACE", "1s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(p.workflow),
	}
}

func (p *armed) Run(t *testing.T, ctx context.Context) {
	p.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, p.workflow.Registry().AddWorkflowN("Armed", func(*task.WorkflowContext) (any, error) {
		return "ran", nil
	}))
	p.workflow.BackendClient(t, ctx)
	gclient := p.workflow.GRPCClient(t, ctx)

	const instanceID = "pending-start-armed"
	created := time.Now().Add(-time.Minute)
	p.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 1, nil, []*protos.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.New(created),
		EventType: &protos.HistoryEvent_ExecutionStarted{
			ExecutionStarted: &protos.ExecutionStartedEvent{
				Name:             "Armed",
				Input:            wrapperspb.String(`"Dapr"`),
				WorkflowInstance: &protos.WorkflowInstance{InstanceId: instanceID},
			},
		},
	}})

	// The start reminder exists but is far from due, as under a Scheduler
	// trigger backlog.
	reminderName := "start-es-" + strconv.FormatInt(created.UnixNano(), 10)
	meta := &schedulerv1pb.JobMetadata{
		Namespace: "default", AppId: p.workflow.Dapr().AppID(),
		Target: &schedulerv1pb.JobTargetMetadata{
			Type: &schedulerv1pb.JobTargetMetadata_Actor{
				Actor: &schedulerv1pb.TargetActorReminder{
					Type: p.workflow.WorkflowActorType(0), Id: instanceID,
				},
			},
		},
	}
	sched := p.workflow.Scheduler().Client(t, ctx)
	_, err := sched.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name:     reminderName,
		Job:      &schedulerv1pb.Job{DueTime: new(time.Now().Add(time.Hour).Format(time.RFC3339))},
		Metadata: meta,
	})
	require.NoError(t, err)
	require.Equal(t, 1, p.workflow.Scheduler().JobKeyCount(t, ctx, "||"+instanceID+"||"+reminderName))

	redriven := func() float64 {
		return p.workflow.Dapr().Metrics(t, ctx).SumWithLabels("dapr_runtime_workflow_local_wake_count", "status:pending_start_redriven")
	}

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: instanceID, WorkflowComponent: "dapr"})
		require.NoError(t, gerr)
		require.Equal(t, "PENDING", resp.GetRuntimeStatus(), "an armed pending start must not be started early")
		require.Zero(t, redriven(), "an armed pending start must not be re-asserted")
		time.Sleep(100 * time.Millisecond)
	}

	// Once the reminder is genuinely missing the next status read past the
	// grace re-asserts it.
	_, err = sched.DeleteJob(ctx, &schedulerv1pb.DeleteJobRequest{Name: reminderName, Metadata: meta})
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, gerr := gclient.GetWorkflowBeta1(ctx, &rtv1.GetWorkflowRequest{InstanceId: instanceID, WorkflowComponent: "dapr"})
		if assert.NoError(c, gerr) {
			assert.Equal(c, "COMPLETED", resp.GetRuntimeStatus())
		}
	}, 30*time.Second, 50*time.Millisecond)
	assert.Positive(t, redriven())
}
