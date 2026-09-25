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

package reuseid

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(staleresultreminder))
}

// staleresultreminder verifies the durable delivery path of a superseded
// activity result: an activity-result reminder whose payload names a
// scheduling other than the one recorded for its task id is consumed and
// dropped, not refired every second for good, and the workflow still
// completes with its own result.
type staleresultreminder struct {
	workflow *workflow.Workflow
}

func (s *staleresultreminder) Setup(t *testing.T) []framework.Option {
	// A superseded result refires across the store's lag until it is older
	// than the publish window, so shorten the window: the planted result
	// carries a fresh timestamp and the job must drain well inside the
	// suite's budget.
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "2s",
		))),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *staleresultreminder) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddWorkflowN("staleresultreminder", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("gated", func(task.ActivityContext) (any, error) {
		markStarted()
		<-release
		return "real", nil
	}))

	client := s.workflow.BackendClient(t, ctx)
	id, err := client.ScheduleNewWorkflow(ctx, "staleresultreminder")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// The recorded scheduling of task 0 carries this execution's id; the
	// planted result names another.
	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	var scheduledExec string
	for _, ev := range hist.GetEvents() {
		if ts := ev.GetTaskScheduled(); ts != nil && ev.GetEventId() == 0 {
			scheduledExec = ts.GetTaskExecutionId()
		}
	}
	require.NotEmpty(t, scheduledExec, "task 0 must be recorded with an execution id")

	// Planted through the scheduler as the activity actor would plant it:
	// a one-shot reminder on the workflow actor with a retry-forever policy.
	appID := s.workflow.Dapr().AppID()
	var schedClient schedulerv1pb.SchedulerClient
	if s.workflow.Signing() {
		schedClient = s.workflow.Scheduler().ClientMTLS(t, ctx, appID)
	} else {
		schedClient = s.workflow.Scheduler().Client(t, ctx)
	}
	const reminderName = common.ReminderPrefixActivityResult + "stale"
	fworkflow.PlantReminder(t, ctx, schedClient, appID, string(id), reminderName, &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				TaskExecutionId: "not-" + scheduledExec,
				Result:          wrapperspb.String(`"stale"`),
			},
		},
	})

	// Consumed, not refired for good.
	s.workflow.Scheduler().WaitJobKeyCount(t, ctx, reminderName, func(n int) bool { return n == 0 })

	releaseOnce()
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	require.Equal(t, `"real"`, meta.GetOutput().GetValue(), "the stale result must not resolve the task")
}
