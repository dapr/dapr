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

package chaos

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(dedupcompleted))
}

// dedupcompleted redelivers an activity result a completed workflow already
// recorded. Nothing will process the workflow's inbox again, so the actor must
// ack the duplicate without arming a wake-up reminder: after a fan-out whose
// activities re-ran, every late redelivery would otherwise cost a reminder
// create, a fire and a turn.
type dedupcompleted struct {
	workflow *workflow.Workflow
	sched    *scheduler.Scheduler
	proxy    *proxy.Proxy
}

func (d *dedupcompleted) Setup(t *testing.T) []framework.Option {
	appID := uuid.New().String()
	sen := sentry.New(t)
	d.sched = scheduler.New(t,
		scheduler.WithSentry(sen),
		scheduler.WithID("dapr-scheduler-server-0"),
	)
	d.proxy = proxy.New(t, d.sched, proxy.WithSentry(t, sen, "default", appID))

	d.workflow = workflow.New(t,
		workflow.WithSentryInstance(sen),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithSchedulerInstance(d.sched),
		workflow.WithSchedulerAddress(d.proxy.Address()),
	)

	return []framework.Option{
		framework.WithProcesses(sen, d.sched, d.proxy, d.workflow),
	}
}

func (d *dedupcompleted) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const wfID = "dedupcompleted-wf"

	r := d.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(actx task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))

	bc := d.workflow.BackendClient(t, ctx)
	gclient := d.workflow.GRPCClient(t, ctx)

	_, err := gclient.StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	// Let the workflow complete naturally so history contains a real
	// TaskScheduled + TaskCompleted pair. The duplicate we inject below has to
	// carry the same TaskScheduledId for dedup to hit on history.
	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	hist, err := bc.GetInstanceHistory(ctx, api.InstanceID(wfID))
	require.NoError(t, err)

	var scheduledID int32 = -1
	for _, he := range hist.GetEvents() {
		if ts := he.GetTaskScheduled(); ts != nil && ts.GetName() == "act" {
			scheduledID = he.GetEventId()
			break
		}
	}
	require.NotEqual(t, int32(-1), scheduledID, "TaskScheduled for 'act' not found in workflow history")

	// Wait until the scheduler has no remaining reminders for the workflow's
	// actor or its activity actors.
	ns := d.workflow.Dapr().Namespace()
	appID := d.workflow.Dapr().AppID()
	wfReminderPrefix := fmt.Sprintf("dapr/jobs/actorreminder||%s||dapr.internal.%s.%s.workflow||%s||", ns, ns, appID, wfID)
	actReminderPrefix := fmt.Sprintf("dapr/jobs/actorreminder||%s||dapr.internal.%s.%s.activity||%s::", ns, ns, appID, wfID)
	require.EventuallyWithT(t, func(co *assert.CollectT) {
		assert.Empty(co, d.sched.ListAllKeys(t, ctx, wfReminderPrefix), "workflow reminders not yet cleared")
		assert.Empty(co, d.sched.ListAllKeys(t, ctx, actReminderPrefix), "activity reminders not yet cleared")
	}, 15*time.Second, 10*time.Millisecond)

	// A wake-up armed for the duplicate is a new-event reminder (the durable
	// wake-up, or the fast path's janitor); other jobs pass.
	armedCh := make(chan struct{})
	d.proxy.ArmNamedFailures(proxy.MethodScheduleJob, "new-event-", 1, codes.Internal, armedCh)

	dup := &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: scheduledID,
				Result:          wrapperspb.String(`"done"`),
			},
		},
	}
	dupBytes, err := proto.Marshal(dup)
	require.NoError(t, err)

	wfActorType := fmt.Sprintf("dapr.internal.%s.%s.workflow", d.workflow.Dapr().Namespace(), d.workflow.Dapr().AppID())
	_, err = d.workflow.InternalGRPCClient(t, ctx).CallActor(ctx,
		internalv1pb.NewInternalInvokeRequest("AddWorkflowEvent").
			WithActor(wfActorType, wfID).
			WithData(dupBytes),
	)
	require.NoError(t, err, "the duplicate must be acked")

	select {
	case <-armedCh:
		require.Fail(t, "a duplicate result for a completed workflow armed a wake-up reminder")
	case <-time.After(3 * time.Second):
	}
	assert.Zero(t, d.proxy.FailedCount(), "no wake-up ScheduleJob expected after the duplicate")
}
