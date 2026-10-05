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

package upgrade

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
	wfupgrade "github.com/dapr/dapr/tests/integration/framework/process/workflow/upgrade"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(stampedresultreminder))
}

// stampedresultreminder hands the release daprd an activity-result reminder
// named the way master names one: with the execution ID of the generation
// that dispatched the activity after the unique part. A master activity host
// can create that reminder and the release daprd fire it, across a downgrade
// or in a mixed-version cluster. The release daprd must read the reminder as
// it always has and resolve the activity from it.
type stampedresultreminder struct {
	upgrade *wfupgrade.Upgrade
}

func (s *stampedresultreminder) Setup(t *testing.T) []framework.Option {
	s.upgrade = wfupgrade.New(t)

	return []framework.Option{
		framework.WithProcesses(s.upgrade),
	}
}

func (s *stampedresultreminder) Run(t *testing.T, ctx context.Context) {
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)
	started := make(chan struct{})
	markStarted := sync.OnceFunc(func() { close(started) })

	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("gated", func(c *task.WorkflowContext) (any, error) {
		var out string
		if err := c.CallActivity("gated").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, reg.AddActivityN("gated", func(task.ActivityContext) (any, error) {
		markStarted()
		<-release
		return "real", nil
	}))

	legacy := s.upgrade.Start(t, ctx, s.upgrade.From(), reg)
	id, err := legacy.ScheduleNewWorkflow(ctx, "gated", api.WithInstanceID("stampedresultreminder"))
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	hist, err := legacy.GetInstanceHistory(ctx, id)
	require.NoError(t, err)
	events := hist.GetEvents()
	scheduled := events[slices.IndexFunc(events, fworkflow.IsTaskScheduledFor(0))].GetTaskScheduled()
	execID := events[slices.IndexFunc(events, func(e *protos.HistoryEvent) bool { return e.GetExecutionStarted() != nil })].
		GetExecutionStarted().GetWorkflowInstance().GetExecutionId().GetValue()
	require.NotEmpty(t, execID)

	name := common.ActivityResultReminderName("stamped", execID)
	fworkflow.PlantActorReminder(t, ctx, s.upgrade.Scheduler(), s.upgrade.Scheduler().Client(t, ctx), s.upgrade.From().AppID(), string(id), name, &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				TaskExecutionId: scheduled.GetTaskExecutionId(),
				Result:          wrapperspb.String(`"from-reminder"`),
			},
		},
	})

	meta, err := legacy.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), "%v", meta.GetFailureDetails())
	assert.Equal(t, `"from-reminder"`, meta.GetOutput().GetValue(), "the release daprd must resolve the activity from the stamped reminder")
}
