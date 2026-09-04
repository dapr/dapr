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

package unstartable

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(foldpublish))
}

type foldpublish struct {
	workflow *workflow.Workflow
}

func (u *foldpublish) Setup(t *testing.T) []framework.Option {
	u.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0,
			daprd.WithFeatureEnabled(t, "WorkflowsFastPath"),
		),
	)
	return []framework.Option{
		framework.WithProcesses(u.workflow),
	}
}

func (u *foldpublish) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	var activityRuns atomic.Int64
	require.NoError(t, u.workflow.Registry().AddActivityN("Emit", func(task.ActivityContext) (any, error) {
		activityRuns.Add(1)
		return "emitted", nil
	}))
	client := u.workflow.BackendClient(t, ctx)

	const instanceID = "lost-start-fold"

	u.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 1, nil, nil)

	invocation, err := anypb.New(&protos.ActivityInvocation{
		HistoryEvent: &protos.HistoryEvent{
			EventId:   0,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: "Emit"},
			},
		},
	})
	require.NoError(t, err)
	job := u.workflow.Scheduler().JobNowActor("run-activity", "default",
		u.workflow.Dapr().AppID(), u.workflow.ActivityActorType(0), instanceID+"::0")
	job.GetJob().Data = invocation
	_, err = u.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx, job)
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		fmeta, ferr := client.FetchWorkflowMetadata(ctx, instanceID)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_FAILED, fmeta.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*10)

	meta, err := client.FetchWorkflowMetadata(ctx, instanceID)
	require.NoError(t, err)
	fd := meta.GetFailureDetails()
	require.NotNil(t, fd)
	assert.Equal(t, staterrors.ErrorTypeUnstartableState, fd.GetErrorType())

	assert.Equal(t, int64(1), activityRuns.Load())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, u.workflow.Scheduler().JobKeyCount(t, ctx, "run-activity"),
			"the settled publish must let the activity delete its reminder")
	}, time.Second*30, time.Millisecond*10)
}
