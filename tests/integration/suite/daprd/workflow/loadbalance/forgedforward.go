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

package loadbalance

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/actors/targets/workflow/common"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(forgedforward))
}

const forgedCount = 6

// forgedforward runs the target app as two replicas behind the placement
// service, so the Scheduler delivers a fired reminder to whichever replica
// it picks and a non-owner forwards it to the owner over the sidecar RPC.
// Activity-result reminders forged by another app in the namespace must be
// dropped by the owner whether they arrive directly or forwarded, while the
// real result of the activity, hosted by a third app, completes the
// workflow.
type forgedforward struct {
	sentry   *sentry.Sentry
	workflow *workflow.Workflow
	dropped  [2]*logline.LogLine
}

func (f *forgedforward) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t)

	opts := make([]workflow.Option, 0, 6+len(f.dropped))
	opts = append(opts,
		workflow.WithDaprds(3),
		workflow.WithSentryInstance(f.sentry),
		workflow.WithSigning(false),
		workflow.WithPlacementService(),
		workflow.WithClusteredDeployment(true),
		workflow.WithDaprdOptions(2, daprd.WithAppID("acthost")),
	)
	for i := range f.dropped {
		f.dropped[i] = logline.New(t, logline.WithCaptureAll())
		opts = append(opts, workflow.WithDaprdOptions(i,
			daprd.WithAppID("target"),
			daprd.WithLogLevel("debug"),
			daprd.WithExecOptions(exec.WithStdout(f.dropped[i].Stdout()), exec.WithStderr(f.dropped[i].Stderr())),
		))
	}
	f.workflow = workflow.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(f.sentry, f.dropped[0], f.dropped[1], f.workflow),
	}
}

func (f *forgedforward) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	block := make(chan struct{})
	started := make(chan struct{}, 1)
	target := task.NewTaskRegistry()
	require.NoError(t, target.AddWorkflowN("Target", func(c *task.WorkflowContext) (any, error) {
		var out string
		err := c.CallActivity("Slow", task.WithActivityAppID("acthost")).Await(&out)
		return out, err
	}))
	acthost := task.NewTaskRegistry()
	require.NoError(t, acthost.AddActivityN("Slow", func(task.ActivityContext) (any, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
		return "real", nil
	}))

	cl := f.workflow.ConnectWorkerN(t, ctx, 0, target).Client
	f.workflow.ConnectWorkerN(t, ctx, 1, target)
	f.workflow.ConnectWorkerN(t, ctx, 2, acthost)
	for i := range 3 {
		f.workflow.WaitForConnectedWorkersN(t, ctx, i, 1)
	}

	id, err := cl.ScheduleNewWorkflow(ctx, "Target")
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(time.Second * 20):
		require.Fail(t, "timed out waiting for the activity to start")
	}

	// The first action of a workflow is task 0. Several reminders so the
	// Scheduler's round robin lands some on each replica.
	other := f.workflow.Scheduler().ClientMTLS(t, ctx, "other")
	for i := range forgedCount {
		forged, aerr := anypb.New(&protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_TaskCompleted{
				TaskCompleted: &protos.TaskCompletedEvent{
					TaskScheduledId: 0,
					Result:          wrapperspb.String(`"FORGED"`),
				},
			},
		})
		require.NoError(t, aerr)
		_, err = other.ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
			Name: fmt.Sprintf("activity-result-forged-%d", i),
			Job: &schedulerv1pb.Job{
				DueTime:       new("0s"),
				Data:          forged,
				FailurePolicy: common.RetryForeverPolicy(),
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
		require.NoError(t, err)
	}

	// Polled from EventuallyWithT goroutines, so no require on t here.
	etcd := f.workflow.Scheduler().ETCDClient(t, ctx)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, logline.CountAll("it was sent by app 'other' but the task was dispatched to 'acthost'", f.dropped[0], f.dropped[1]), forgedCount)
		resp, gerr := etcd.Get(ctx, "dapr/jobs", clientv3.WithPrefix())
		if !assert.NoError(c, gerr) {
			return
		}
		// Keys end in `||<reminder name>`, so match the exact names.
		for _, kv := range resp.Kvs {
			for i := range forgedCount {
				assert.False(c, strings.HasSuffix(string(kv.Key), fmt.Sprintf("||activity-result-forged-%d", i)),
					"every forged reminder must be acked and deleted, not retried: %s", kv.Key)
			}
		}
	}, time.Second*30, time.Millisecond*10)

	meta, err := cl.FetchWorkflowMetadata(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_RUNNING, meta.GetRuntimeStatus(), "no forged result may complete the workflow")

	close(block)
	meta, err = cl.WaitForWorkflowCompletion(ctx, id, api.WithFetchPayloads(true))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, `"real"`, meta.GetOutput().GetValue())
}
