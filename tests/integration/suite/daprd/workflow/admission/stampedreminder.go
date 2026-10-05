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
	"slices"
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
	suite.Register(new(stampedreminder))
}

// stampedreminder delivers an activity result through an activity-result
// reminder whose name carries an execution ID other than the workflow's: the
// result of an activity another execution dispatched, re-delivered by
// reminder after the workflow continued as new or was recreated under the
// same ID. Every other field matches the outstanding scheduling, so the
// execution ID alone must reject it, the same as one delivered by a direct
// call.
type stampedreminder struct {
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (s *stampedreminder) Setup(t *testing.T) []framework.Option {
	s.logline = logline.New(t, logline.WithCaptureAll())
	s.workflow = workflow.New(t,
		// Signing mode opt-out: the planted result carries no attestation.
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0,
			daprd.WithExecOptions(
				exec.WithStdout(s.logline.Stdout()),
				exec.WithStderr(s.logline.Stderr()),
			),
		),
	)

	return []framework.Option{
		framework.WithProcesses(s.logline, s.workflow),
	}
}

func (s *stampedreminder) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	const id = "stampedreminder"

	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })

	reg := s.workflow.Registry()
	require.NoError(t, reg.AddActivityN("gated", func(task.ActivityContext) (any, error) {
		markStarted()
		<-release
		return "real", nil
	}))
	require.NoError(t, reg.AddWorkflowN("stampedreminder", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	client := s.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "stampedreminder", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	hist, err := client.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	events := hist.GetEvents()
	scheduled := events[slices.IndexFunc(events, fworkflow.IsTaskScheduledFor(0))].GetTaskScheduled()

	fworkflow.PlantReminder(t, ctx, s.workflow, id, common.ActivityResultReminderName("stamped", "another-execution"), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				TaskExecutionId: scheduled.GetTaskExecutionId(),
				Result:          wrapperspb.String(`"another-execution"`),
			},
		},
	})

	dropped := "Workflow actor '" + id + "': dropping completion (sender ''): it was created under a previous execution"
	require.Eventually(t, func() bool { return s.logline.Count(dropped) >= 1 }, time.Second*20, time.Millisecond*10,
		"a result stamped with another execution must be dropped")

	releaseOnce()
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"real"`, meta.GetOutput().GetValue(), "the workflow must complete from its own activity's result")
}
