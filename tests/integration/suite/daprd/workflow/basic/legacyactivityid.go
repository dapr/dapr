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

package basic

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(legacyactivityid))
}

// legacyactivityid invokes the activity actor directly with a bare
// TaskScheduled event under a three-part
// "<instanceID>::<taskID>::<generation>" ID with a non-zero generation, as a
// 1.18 orchestrator writes, and checks the result reaches the parent and the
// reminder is acked. The real dispatch is parked so only the planted delivery
// can complete the workflow.
type legacyactivityid struct {
	workflow *workflow.Workflow
}

func (l *legacyactivityid) Setup(t *testing.T) []framework.Option {
	l.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(l.workflow),
	}
}

func (l *legacyactivityid) Run(t *testing.T, ctx context.Context) {
	l.workflow.WaitUntilRunning(t, ctx)

	var entries atomic.Int32
	l.workflow.Registry().AddWorkflowN("withactivity", func(wctx *task.WorkflowContext) (any, error) {
		var out string
		if err := wctx.CallActivity("echo", task.WithActivityInput("in")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	})
	l.workflow.Registry().AddActivityN("echo", func(actx task.ActivityContext) (any, error) {
		if entries.Add(1) == 1 {
			<-actx.Context().Done()
			return nil, actx.Context().Err()
		}
		var in string
		if err := actx.GetInput(&in); err != nil {
			return nil, err
		}
		return in + "-done", nil
	})
	cl := l.workflow.BackendClient(t, ctx)

	const wfID = "legacy-activity-id"
	_, err := cl.ScheduleNewWorkflow(ctx, "withactivity", api.WithInstanceID(wfID))
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return entries.Load() == 1
	}, 10*time.Second, 10*time.Millisecond)

	var scheduled *protos.HistoryEvent
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		hist, herr := cl.GetInstanceHistory(ctx, wfID)
		if !assert.NoError(c, herr) {
			return
		}
		for _, e := range hist.GetEvents() {
			if e.GetTaskScheduled() != nil {
				scheduled = e
				return
			}
		}
		assert.Fail(c, "TaskScheduled not yet in history")
	}, 10*time.Second, 10*time.Millisecond)

	data, err := proto.Marshal(scheduled)
	require.NoError(t, err)

	legacyActorID := fmt.Sprintf("%s::%d::3", wfID, scheduled.GetEventId())
	_, err = l.workflow.GRPCClient(t, ctx).InvokeActor(ctx, &rtv1.InvokeActorRequest{
		ActorType: l.workflow.ActivityActorType(0),
		ActorId:   legacyActorID,
		Method:    "Execute",
		Data:      data,
	})
	require.NoError(t, err)

	meta, err := cl.WaitForWorkflowCompletion(ctx, wfID)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"in-done"`, meta.GetOutput().GetValue())
	assert.Equal(t, int32(2), entries.Load())

	// The parked real dispatch keeps its own reminder alive; only the legacy
	// one must have been acked rather than left retrying.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Zero(c, l.workflow.Scheduler().JobKeyCount(t, ctx, "||"+legacyActorID+"||run-activity"))
	}, 10*time.Second, 10*time.Millisecond)
}
