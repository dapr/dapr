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

package reuseid

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	suite.Register(new(forged))
}

// forged completes a workflow while one of its activities is still running,
// so its ID is held until the real result arrives, then has another app in
// the namespace forge that result through an activity-result reminder. The
// forged result is dropped and must not release the ID: a create reusing it
// is still refused until the real result lands.
type forged struct {
	sentry   *sentry.Sentry
	workflow *workflow.Workflow
	logline  *logline.LogLine
}

func (f *forged) Setup(t *testing.T) []framework.Option {
	f.sentry = sentry.New(t)
	f.logline = logline.New(t, logline.WithCaptureAll())
	f.workflow = workflow.New(t,
		workflow.WithSentryInstance(f.sentry),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID("target"),
			daprd.WithNamespace("default"),
			daprd.WithLogLevel("debug"),
			daprd.WithExecOptions(exec.WithStdout(f.logline.Stdout()), exec.WithStderr(f.logline.Stderr())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(f.sentry, f.logline, f.workflow),
	}
}

func (f *forged) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	const id = api.InstanceID("reuse-forged")
	release := make(chan struct{})
	releaseOnce := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseOnce)

	reg := f.workflow.Registry()
	require.NoError(t, reg.AddActivityN("slow", func(task.ActivityContext) (any, error) {
		<-release
		return "late", nil
	}))
	require.NoError(t, reg.AddWorkflowN("forged", func(wctx *task.WorkflowContext) (any, error) {
		// Scheduled and never awaited: the workflow completes with it running.
		wctx.CallActivity("slow")
		return "done", nil
	}))

	client := f.workflow.BackendClient(t, ctx)
	_, err := client.ScheduleNewWorkflow(ctx, "forged", api.WithInstanceID(id))
	require.NoError(t, err)
	meta, err := client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// The first action of a workflow is task 0.
	data, err := anypb.New(&protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{
			TaskCompleted: &protos.TaskCompletedEvent{
				TaskScheduledId: 0,
				Result:          wrapperspb.String(`"FORGED"`),
			},
		},
	})
	require.NoError(t, err)
	_, err = f.workflow.Scheduler().ClientMTLS(t, ctx, "other").ScheduleJob(ctx, &schedulerv1pb.ScheduleJobRequest{
		Name: "activity-result-forged",
		Job: &schedulerv1pb.Job{
			DueTime:       new("0s"),
			Data:          data,
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

	count := func(needle string) int { return logline.CountAll(needle, f.logline) }
	dropped := fmt.Sprintf("Workflow actor '%s': dropping completion (sender ''): it was sent by app 'other' but the task was dispatched to 'target'", id)
	require.Eventually(t, func() bool { return count(dropped) >= 1 }, time.Second*20, time.Millisecond*10,
		"the forged result must be dropped by the creator check")

	_, err = client.ScheduleNewWorkflow(ctx, "forged", api.WithInstanceID(id))
	require.Error(t, err, "the forged result must not release the ID while the real result is still in flight")
	assert.Contains(t, err.Error(), "already awaiting an activity result")

	// The real result reaches the completed workflow and is dropped; the ID
	// is then reusable.
	settled := fmt.Sprintf("Workflow actor '%s': dropping completion (sender ''): the workflow has completed", id)
	releaseOnce()
	require.Eventually(t, func() bool { return count(settled) >= 1 }, time.Second*20, time.Millisecond*10,
		"the real result must be dropped by the completed workflow")
	_, err = client.ScheduleNewWorkflow(ctx, "forged", api.WithInstanceID(id))
	require.NoError(t, err, "reusing the ID after the real result must succeed")
	meta, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
