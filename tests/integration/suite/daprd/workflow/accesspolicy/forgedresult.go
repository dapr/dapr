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

package accesspolicy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(forgedresult))
}

// forgedresult asserts that activity-result reminders created on a target's
// workflow actor by another app in the namespace (which the Scheduler allows,
// since activity hosts deliver cross-app results that way) never move the
// workflow: a result for the outstanding task is dropped because its creator
// is not the app the activity was dispatched to; a payload that is not an
// activity result (a termination, a timer firing) is dropped before it can
// reach the inbox; a result for a task the workflow has not scheduled yet is
// refused until the task is scheduled and then dropped by the creator check,
// unless its creator stamped it outside the retry window, in which case it
// is dropped on the first fire.
// Every forged reminder is acked and deleted, the sidecar stays up, and the
// workflow completes with the real activity outputs.
type forgedresult struct {
	sentry  *sentry.Sentry
	place   *placement.Placement
	sched   *scheduler.Scheduler
	target  *daprd.Daprd
	dropped *logline.LogLine
}

func (f *forgedresult) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t)
	f.place = placement.New(t, placement.WithSentry(t, f.sentry))
	f.sched = scheduler.New(t, scheduler.WithSentry(f.sentry), scheduler.WithID("dapr-scheduler-server-0"))

	f.dropped = logline.New(t, logline.WithStdoutLineContains(
		"dropping completion (sender ''): it was sent by app 'other' but the task was dispatched to 'target'",
		"dropping activity-result reminder 'activity-result-forged-terminate' from app 'other': payload is not an activity result",
		"dropping activity-result reminder 'activity-result-forged-timer' from app 'other': payload is not an activity result",
		"dropping activity-result reminder 'activity-result-forged-ahead-future', its scheduling did not become durable",
	))

	f.target = daprd.New(t,
		daprd.WithAppID("target"),
		daprd.WithNamespace("default"),
		daprd.WithLogLevel("debug"),
		daprd.WithExecOptions(exec.WithStdout(f.dropped.Stdout())),
		daprd.WithInMemoryActorStateStore("statestore"),
		daprd.WithPlacementAddresses(f.place.Address()),
		daprd.WithSchedulerAddresses(f.sched.Address()),
		daprd.WithSentry(t, f.sentry),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentry, f.place, f.sched, f.dropped, f.target),
	}
}

func (f *forgedresult) Run(t *testing.T, ctx context.Context) {
	f.place.WaitUntilRunning(t, ctx)
	f.sched.WaitUntilRunning(t, ctx)
	f.target.WaitUntilRunning(t, ctx)

	block := make(chan struct{})
	started := make(chan struct{}, 1)
	reg := task.NewTaskRegistry()
	require.NoError(t, reg.AddWorkflowN("Target", func(c *task.WorkflowContext) (any, error) {
		var first, second string
		if err := c.CallActivity("Slow").Await(&first); err != nil {
			return nil, err
		}
		if err := c.CallActivity("Second").Await(&second); err != nil {
			return nil, err
		}
		return first + "+" + second, nil
	}))
	require.NoError(t, reg.AddActivityN("Slow", func(task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return "real", nil
	}))
	require.NoError(t, reg.AddActivityN("Second", func(task.ActivityContext) (any, error) {
		return "second", nil
	}))

	cl := client.NewTaskHubGrpcClient(f.target.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cl.StartWorkItemListener(ctx, reg))

	id, err := cl.ScheduleNewWorkflow(ctx, "Target")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// The first action of a workflow is task 0; the second activity, not
	// scheduled until the first completes, will be task 1.
	completed := func(taskID int32, result string) *protos.HistoryEvent {
		return &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TaskCompleted{
				TaskCompleted: &protos.TaskCompletedEvent{
					TaskScheduledId: taskID,
					Result:          wrapperspb.String(result),
				},
			},
		}
	}
	forged := map[string]*protos.HistoryEvent{
		"activity-result-forged": completed(0, `"FORGED"`),
		"activity-result-forged-terminate": {
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionTerminated{
				ExecutionTerminated: &protos.ExecutionTerminatedEvent{Input: wrapperspb.String(`"FORGED"`)},
			},
		},
		"activity-result-forged-timer": {
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TimerFired{
				TimerFired: &protos.TimerFiredEvent{TimerId: 3, FireAt: timestamppb.Now()},
			},
		},
		"activity-result-forged-ahead": completed(1, `"FORGED1"`),
		// The creator picks the stamp that bounds the retry of a refused
		// result: one far in the future must expire like an ancient one, not
		// keep the reminder refiring forever.
		"activity-result-forged-ahead-future": completed(1, `"FORGED1"`),
	}
	forged["activity-result-forged-ahead-future"].Timestamp = timestamppb.New(time.Now().Add(time.Hour))

	other := f.sched.ClientMTLS(t, ctx, "other")
	for name, ev := range forged {
		data, aerr := anypb.New(ev)
		require.NoError(t, aerr)
		_, err = other.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: name,
			Job: &schedulerv1pb.Job{
				DueTime: new("0s"),
				Data:    data,
				FailurePolicy: &commonv1pb.JobFailurePolicy{
					Policy: &commonv1pb.JobFailurePolicy_Constant{
						Constant: &commonv1pb.JobFailurePolicyConstant{Interval: durationpb.New(time.Second)},
					},
				},
			},
			Metadata: &schedulerv1pb.JobMetadata{
				AppId:     "other",
				Namespace: "default",
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{
							Type: "dapr.internal.default.target.workflow",
							Id:   string(id),
						},
					},
				},
			},
		})
		require.NoError(t, err, "the scheduler allows creating activity result reminders on another app's workflow actor: %s", name)
	}

	// Polled from EventuallyWithT goroutines, so no require on t here. Keys
	// end in `||<reminder name>`: compare the exact name, not a substring.
	etcd := f.sched.ETCDClient(t, ctx)
	jobNames := func() ([]string, error) {
		resp, gerr := etcd.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		if gerr != nil {
			return nil, gerr
		}
		names := make([]string, 0, len(resp.Kvs))
		for _, kv := range resp.Kvs {
			key := string(kv.Key)
			names = append(names, key[strings.LastIndex(key, "||")+2:])
		}
		return names, nil
	}

	f.dropped.EventuallyFoundAll(t)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		names, jerr := jobNames()
		if !assert.NoError(c, jerr) {
			return
		}
		for _, name := range []string{"activity-result-forged", "activity-result-forged-terminate", "activity-result-forged-timer", "activity-result-forged-ahead-future"} {
			assert.NotContains(c, names, name, "the forged reminder must be acked and deleted, not retried")
		}
	}, time.Second*20, time.Millisecond*10)
	// The result for the not yet scheduled task is refused, not acked, so
	// the Scheduler keeps re-delivering it while its scheduling could still
	// be committing.
	names, err := jobNames()
	require.NoError(t, err)
	assert.Contains(t, names, "activity-result-forged-ahead")

	meta, err := cl.FetchWorkflowMetadata(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus(), "no forged event may move the workflow")

	close(block)
	meta, err = cl.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"real+second"`, meta.GetOutput().GetValue())

	// Once task 1 is scheduled the re-delivered result fails the creator
	// check and is acked, so the one-shot reminder goes away.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		names, jerr := jobNames()
		if assert.NoError(c, jerr) {
			assert.NotContains(c, names, "activity-result-forged-ahead")
		}
	}, time.Second*20, time.Millisecond*10)
}
