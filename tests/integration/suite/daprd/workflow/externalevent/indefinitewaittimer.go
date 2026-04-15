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

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(indefiniteWaitTimer))
}

// indefiniteWaitTimer covers an indefinite `WaitForSingleEvent` followed by
// a user `CreateTimer`. The legacy history records the user timer at
// `EventId=0`. On replay the current SDK has an optional CreateTimer pending
// at id=0; the incoming TimerCreated is a different, non-optional timer at
// id=0. The SDK must detect this and drop the optional timer even when both
// pending and incoming are CreateTimer.
type indefiniteWaitTimer struct {
	workflow *workflow.Workflow
}

func (i *indefiniteWaitTimer) Setup(t *testing.T) []framework.Option {
	i.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *indefiniteWaitTimer) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	i.workflow.Registry().AddWorkflowN("indefwaitTimer", func(wctx *task.WorkflowContext) (any, error) {
		if err := wctx.WaitForSingleEvent("foo", -1).Await(nil); err != nil {
			return nil, err
		}
		// Legacy history records a user CreateTimer at EventId=0 that has
		// already fired (TimerFired in history, too).
		if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if err := wctx.WaitForSingleEvent("done", time.Hour).Await(nil); err != nil {
			return nil, err
		}
		return nil, nil
	})

	cl := i.workflow.BackendClient(t, ctx)

	instanceID := "indefwait-timer"
	appID := i.workflow.Dapr().AppID()
	actorType := fmt.Sprintf("dapr.internal.default.%s.workflow", appID)

	injectLegacyState(t, ctx, i.workflow.DB().GetConnection(t), i.workflow.DB().GetTableName(t),
		appID, actorType, instanceID,
		legacyUserTimerHistory(instanceID, "indefwaitTimer"),
	)

	require.NoError(t, cl.RaiseEvent(ctx, api.InstanceID(instanceID), "done"))

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := cl.WaitForWorkflowCompletion(waitCtx, api.InstanceID(instanceID))
	require.NoError(t, err, "workflow must complete when replaying legacy history containing a non-optional timer at id=0; this will fail until durabletask-go includes the backwards-compat fix")
	assert.Equal(t, "ORCHESTRATION_STATUS_COMPLETED", meta.GetRuntimeStatus().String())
}

// legacyUserTimerHistory builds the history an older runtime would have
// persisted for the sequence:
//
//	WaitForSingleEvent("foo", -1)  // no timer emitted by older code
//	CreateTimer(1ms)               // timer created at EventId=0 and already fired
func legacyUserTimerHistory(instanceID, wfName string) []*protos.HistoryEvent {
	iid := &protos.WorkflowInstance{InstanceId: instanceID}
	start := time.Now().UTC().Add(-time.Minute)
	fireAt := start.Add(2 * time.Second)

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
			// Legacy numbering: user CreateTimer lands at id=0. The origin
			// is CreateTimer (not ExternalEvent) and the fireAt is a normal
			// near-future time, so it is NOT an optional timer.
			EventId:   0,
			Timestamp: timestamppb.New(start.Add(2 * time.Second)),
			EventType: &protos.HistoryEvent_TimerCreated{
				TimerCreated: &protos.TimerCreatedEvent{
					FireAt: timestamppb.New(fireAt),
					Origin: &protos.TimerCreatedEvent_CreateTimer{
						CreateTimer: &protos.TimerOriginCreateTimer{},
					},
				},
			},
		},
		{
			EventId:   -1,
			Timestamp: timestamppb.New(fireAt),
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{
					TimerId: 0,
					FireAt:  timestamppb.New(fireAt),
				},
			},
		},
	}
}
