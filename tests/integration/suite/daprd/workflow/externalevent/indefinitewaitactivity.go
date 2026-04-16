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

package externalevent

import (
	"context"
	"fmt"
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
	suite.Register(new(indefiniteWaitActivity))
}

// indefiniteWaitActivity verifies that a workflow whose history was produced
// by an older SDK release (one that did NOT emit an optional timer for
// `WaitForSingleEvent(name, timeout<0)`) can still be replayed correctly by
// the current runtime.
//
// The scenario: an indefinite wait followed by a CallActivity. In legacy
// histories the activity's `TaskScheduled` event carries `EventId=0` because
// the older SDK never reserved an id for the wait. The current SDK emits a
// CreateTimer action at id=0 for that wait, which must be dropped on replay
// so the `TaskScheduled(id=0)` match succeeds. If the SDK does not drop the
// optional timer, replay fails with a non-determinism error and the workflow
// never completes.
type indefiniteWaitActivity struct {
	workflow *workflow.Workflow
}

func (i *indefiniteWaitActivity) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *indefiniteWaitActivity) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	// Orchestrator:
	//   WaitForSingleEvent("foo", -1)   -- resolved by pre-injected EventRaised
	//   CallActivity("A")               -- resolved by pre-injected TaskScheduled + TaskCompleted
	//   WaitForSingleEvent("done", 1h)  -- resolved by the event we raise below
	i.workflow.Registry().AddWorkflowN("indefwaitActivity", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.WaitForSingleEvent("foo", -1).Await(nil); err != nil {
			return nil, err
		}
		var result string
		if err := wctx.CallActivity("A").Await(&result); err != nil {
			return nil, err
		}
		if err := wctx.WaitForSingleEvent("done", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return result, nil
	})
	// Activity code is only invoked if the workflow is ever re-scheduled.
	// In our scenario the activity has already completed in the legacy
	// history, so this registration is only here to satisfy the work-item
	// listener.
	i.workflow.Registry().AddActivityN("A", func(task.ActivityContext) (any, error) {
		return "activity-result", nil
	})

	cl := i.workflow.BackendClient(t, ctx)

	instanceID := "indefwait-activity"
	appID := i.workflow.Dapr().AppID()
	actorType := fmt.Sprintf("dapr.internal.default.%s.workflow", appID)

	injectLegacyState(t, ctx, i.workflow.DB().GetConnection(t), i.workflow.DB().GetTableName(t),
		appID, actorType, instanceID,
		legacyActivityHistory(instanceID, "indefwaitActivity"),
	)

	// Raise "done" — this activates the workflow actor, which loads the
	// pre-injected state and replays. Replay succeeds only if the SDK drops
	// the optional external-event timer before matching the TaskScheduled
	// event at id=0.
	require.NoError(t, cl.RaiseEvent(ctx, api.InstanceID(instanceID), "done"))

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := cl.WaitForWorkflowCompletion(waitCtx, api.InstanceID(instanceID))
	require.NoError(t, err, "workflow must complete when replaying legacy history; this will fail until durabletask-go includes the backwards-compat fix")
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
}

// legacyActivityHistory builds the history an older runtime would have
// persisted for the sequence:
//
//	WaitForSingleEvent("foo", -1)  // no timer emitted by older code
//	CallActivity("A")              // scheduled at EventId=0
//
// with the activity already completed.
func legacyActivityHistory(instanceID, wfName string) []*protos.HistoryEvent {
	iid := &protos.WorkflowInstance{InstanceId: instanceID}
	start := time.Now().UTC().Add(-time.Minute)

	return []*protos.HistoryEvent{
		{
			EventId:   -1,
			Timestamp: timestamppb.New(start),
			EventType: &protos.HistoryEvent_WorkflowStarted{
				WorkflowStarted: &protos.WorkflowStartedEvent{},
			},
		},
		{
			EventId:   -1,
			Timestamp: timestamppb.New(start),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name:             wfName,
					WorkflowInstance: iid,
				},
			},
		},
		{
			EventId:   -1,
			Timestamp: timestamppb.New(start.Add(time.Second)),
			EventType: &protos.HistoryEvent_EventRaised{
				EventRaised: &protos.EventRaisedEvent{Name: "foo"},
			},
		},
		{
			// Older runtime reserved no id for the indefinite wait, so the
			// activity's TaskScheduled lands at id=0.
			EventId:   0,
			Timestamp: timestamppb.New(start.Add(2 * time.Second)),
			EventType: &protos.HistoryEvent_TaskScheduled{
				TaskScheduled: &protos.TaskScheduledEvent{Name: "A"},
			},
		},
		{
			EventId:   -1,
			Timestamp: timestamppb.New(start.Add(3 * time.Second)),
			EventType: &protos.HistoryEvent_TaskCompleted{
				TaskCompleted: &protos.TaskCompletedEvent{
					TaskScheduledId: 0,
					Result:          wrapperspb.String(`"activity-result"`),
				},
			},
		},
	}
}
