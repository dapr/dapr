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

package admission

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(lagreminder))
}

// lagreminder pins the activity-result reminder as a durable retry that
// outlasts the store lag it exists for. The reminder is the retry chain for
// a result that never got its sender's in-hand window, and it retries
// forever, so a refusal it re-judges twice back to back inside one fire and
// then acks cannot span a lag measured in seconds. The refusal must keep
// being returned, on a dropped cache, while the result is younger than the
// publish retry window.
//
// The lag is simulated the only way a single host can: the planted result
// names a scheduling of task 0 the durable history does not record yet, and
// the history row is rewritten to name it once the reminder has demonstrably
// refired. The reminder that lands after the rewrite admits the result.
type lagreminder struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (l *lagreminder) Setup(t *testing.T) []framework.Option {
	l.logline = logline.New(t, logline.WithCaptureAll())
	l.workflow = workflow.New(t,
		workflow.WithFastPath(false),
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(
				exec.WithStdout(l.logline.Stdout()),
				exec.WithStderr(l.logline.Stderr()),
				// The orchestrator-side age bound must span the simulated lag.
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "10s"),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(l.logline, l.workflow),
	}
}

func (l *lagreminder) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	const id = "lagreminder"
	const reminderName = common.ReminderPrefixActivityResult + "lag"

	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })

	reg := l.workflow.Registry()
	require.NoError(t, reg.AddActivityN("gated", func(task.ActivityContext) (any, error) {
		markStarted()
		<-release
		return "real", nil
	}))
	require.NoError(t, reg.AddWorkflowN("lagreminder", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := l.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "lagreminder", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// The activity stays gated for the whole case, so the only resolution of
	// task 0 is the planted one.
	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	var execReal string
	for _, ev := range hist.GetEvents() {
		if ts := ev.GetTaskScheduled(); ts != nil && ev.GetEventId() == 0 {
			execReal = ts.GetTaskExecutionId()
		}
	}
	require.NotEmpty(t, execReal, "task 0 must be recorded with an execution id")
	execPeer := "peer-" + execReal

	appID := l.workflow.Dapr().AppID()
	fworkflow.PlantReminder(t, ctx, l.workflow.Scheduler().Client(t, ctx), appID, id, reminderName, &protos.HistoryEvent{
		EventId: -1,
		// The orchestrator's age bound reads this field, not the reminder's.
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				TaskExecutionId: execPeer,
				Result:          wrapperspb.String(`"planted"`),
			},
		},
	})

	fireNeedle := "Workflow actor '" + id + "': invoking reminder '" + reminderName + "'"
	fires := func() int { return l.logline.Count(fireNeedle) }
	require.Eventually(t, func() bool { return fires() >= 2 }, time.Second*20, time.Millisecond*10,
		"a refusal younger than the publish window must refire the reminder, not be acked on its first judgement")

	// The store catches up: the durable history now records task 0 under the
	// scheduling the planted result resolves, and the metadata row carries a
	// new ETag.
	rows := fworkflow.SQLiteRows(l.workflow.DB(), id)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		_, ev := fworkflow.TaskScheduledRow(t, ctx, rows, 0)
		assert.NotNil(c, ev, "task 0 must be recorded in history")
	}, time.Second*20, time.Millisecond*10)
	fworkflow.SetTaskExecutionID(t, ctx, rows, 0, execPeer)

	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err, "the refire that lands after the lag must admit the result")
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.JSONEq(t, `"planted"`, meta.GetOutput().GetValue())
	l.workflow.Scheduler().WaitJobKeyCount(t, ctx, reminderName, func(n int) bool { return n == 0 })
	assert.GreaterOrEqual(t, fires(), 2, "the reminder must have spanned the lag rather than one fire")
}
