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
	suite.Register(new(orphanreminder))
}

// orphanreminder pins the other half of the activity-result reminder's
// contract: the refusals it refires across are bounded. A completion whose
// task id this generation never scheduled is refused as not-yet-durable,
// which the reminder handler did not classify at all, so a retry-forever
// reminder carrying one refired for good: a permanent storm of full state
// loads under the actor's turn lock, several a second, for an event no
// history will ever admit. It must refire while the result is young, then
// ack once the result is older than the publish retry window.
//
// History signing is deliberate: the not-yet-durable verdict is produced only
// inside the signed verify arm, and that arm short-circuits on an unscheduled
// task id before any signature is read. The planted completion therefore
// carries an empty attestation rather than none, which the arm ahead of it
// would reject as tampering.
type orphanreminder struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (o *orphanreminder) Setup(t *testing.T) []framework.Option {
	o.logline = logline.New(t, logline.WithCaptureAll())
	o.workflow = workflow.New(t,
		workflow.WithHistorySigning(t),
		workflow.WithFastPath(false),
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(
				exec.WithStdout(o.logline.Stdout()),
				exec.WithStderr(o.logline.Stderr()),
				exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_RETRY_WINDOW", "2s"),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(o.logline, o.workflow),
	}
}

func (o *orphanreminder) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	const id = "orphanreminder"
	const reminderName = common.ReminderPrefixActivityResult + "orphan"

	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })

	reg := o.workflow.Registry()
	require.NoError(t, reg.AddActivityN("gated", func(task.ActivityContext) (any, error) {
		markStarted()
		<-release
		return "real", nil
	}))
	require.NoError(t, reg.AddWorkflowN("orphanreminder", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := o.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "orphanreminder", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// Task 7 is absent from history and above every id it records, so the
	// verdict is not-yet-durable on every fire. The attestation is present
	// but empty: the signed arm rejects a completion carrying none outright
	// (and tombstones the workflow for tampering), while one that carries an
	// attestation is matched against the signed history first, and an
	// unscheduled task id short-circuits there before any signature is read.

	appID := o.workflow.Dapr().AppID()
	fworkflow.PlantReminder(t, ctx, o.workflow.Scheduler().ClientMTLS(t, ctx, appID), appID, id, reminderName, &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 7,
				TaskExecutionId: "orphan-exec",
				Result:          wrapperspb.String(`"orphan"`),
				Attestation:     new(protos.ActivityCompletionAttestation),
			},
		},
	})

	fireNeedle := "Workflow actor '" + id + "': invoking reminder '" + reminderName + "'"
	require.Eventually(t, func() bool {
		return o.logline.Count(fireNeedle) >= 2
	}, time.Second*20, time.Millisecond*10, "the bounded arm must still refire across the lag it exists for")
	o.workflow.Scheduler().WaitJobKeyCount(t, ctx, reminderName, func(n int) bool { return n == 0 })

	releaseOnce()
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.JSONEq(t, `"real"`, meta.GetOutput().GetValue())

	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	for _, ev := range hist.GetEvents() {
		assert.NotEqual(t, int32(7), ev.GetTaskCompleted().GetTaskScheduledId(),
			"the orphan result must never enter history")
	}
}
