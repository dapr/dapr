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
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	staterrors "github.com/dapr/dapr/pkg/runtime/wfengine/state/errors"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(signed))
}

type signed struct {
	workflow *workflow.Workflow
}

func (u *signed) Setup(t *testing.T) []framework.Option {
	u.workflow = workflow.New(t, workflow.WithHistorySigning(t))
	return []framework.Option{
		framework.WithProcesses(u.workflow),
	}
}

func (u *signed) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	var runs atomic.Int64
	u.workflow.Registry().AddWorkflowN("unstartable", func(ctx *task.WorkflowContext) (any, error) {
		runs.Add(1)
		return nil, nil
	})

	client := u.workflow.BackendClient(t, ctx)

	const instanceID = "lost-start-signed"

	u.workflow.WriteWorkflowState(t, ctx, 0, instanceID, 1, nil, []*protos.HistoryEvent{{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				Result:          wrapperspb.String(`"orphaned"`),
			},
		},
	}})

	meta, err := client.FetchWorkflowMetadata(ctx, instanceID)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_PENDING, meta.GetRuntimeStatus(),
		"precondition: the fabricated shape reads as PENDING")

	_, err = u.workflow.Scheduler().ClientMTLS(t, ctx, u.workflow.Dapr().AppID()).ScheduleJob(ctx,
		u.workflow.Scheduler().JobNowActor("new-event-orphan", "default",
			u.workflow.Dapr().AppID(), u.workflow.WorkflowActorType(0), instanceID))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		fmeta, ferr := client.FetchWorkflowMetadata(ctx, instanceID)
		if !assert.NoError(c, ferr) {
			return
		}
		assert.Equal(c, api.RUNTIME_STATUS_FAILED, fmeta.GetRuntimeStatus())
	}, time.Second*30, time.Millisecond*10)

	meta, err = client.FetchWorkflowMetadata(ctx, instanceID)
	require.NoError(t, err)
	fd := meta.GetFailureDetails()
	require.NotNil(t, fd)
	assert.Equal(t, staterrors.ErrorTypeUnstartableState, fd.GetErrorType())
	assert.Contains(t, fd.GetErrorMessage(), "no pending ExecutionStarted")

	assert.Zero(t, u.workflow.DB().CountStateKeys(t, ctx, instanceID+"||inbox"))
	assert.Zero(t, runs.Load())
}
