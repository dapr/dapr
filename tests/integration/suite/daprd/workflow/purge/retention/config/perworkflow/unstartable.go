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
package perworkflow

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(unstartable))
}

// unstartable: an instance failed for a lost start event has no workflow
// name, so the app-wide failed TTL applies and no entry can match it.
type unstartable struct {
	workflow *workflow.Workflow
}

func (u *unstartable) Setup(t *testing.T) []framework.Option {
	u.workflow = workflow.New(t,
		workflow.WithSigning(false),
		workflow.WithDaprdOptions(0, policy(t, `
      failed: "2s"
      workflows:
        - name: unstartable
          failed: "1h"
`)),
	)
	return []framework.Option{framework.WithProcesses(u.workflow)}
}

func (u *unstartable) Run(t *testing.T, ctx context.Context) {
	u.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, u.workflow.Registry().AddWorkflowN("unstartable", func(*task.WorkflowContext) (any, error) {
		return nil, nil
	}))
	cl := u.workflow.BackendClient(t, ctx)
	rows := rowCounter(t, ctx, u.workflow)

	const instanceID = "lost-start"
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
	_, err := u.workflow.Scheduler().Client(t, ctx).ScheduleJob(ctx,
		u.workflow.Scheduler().JobNowActor("new-event-orphan", "default",
			u.workflow.Dapr().AppID(), u.workflow.WorkflowActorType(0), instanceID))
	require.NoError(t, err)

	// Purged implies failed: FAILED is the only terminal state this shape
	// can reach.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		meta, err := cl.FetchWorkflowMetadata(ctx, instanceID)
		if errors.Is(err, api.ErrInstanceNotFound) {
			return
		}
		if assert.NoError(c, err) {
			assert.Equal(c, api.RUNTIME_STATUS_FAILED, meta.GetRuntimeStatus())
		}
	}, time.Second*30, time.Millisecond*10)
	waitPurged(t, rows, instanceID, "the app-wide failed TTL did not purge the nameless instance")
}
