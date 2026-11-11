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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	internalv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/framework/process/sentry"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(drainedwakeup))
}

// drainedwakeup drives wake-ups into a running workflow whose inbox is
// already empty, as a fan-out leaves about one per activity once a batched
// turn has consumed the results. Each must ack against the cached state, not
// reload the whole history from the store under the actor lock: at thousands
// of activities those reloads are quadratic and starve every other call on
// the workflow, status streams included.
type drainedwakeup struct {
	workflow *workflow.Workflow
	store    *fault.Store
	logline  *logline.LogLine
}

func (d *drainedwakeup) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	sen := sentry.New(t)
	d.store = fault.New(t)
	sock := socket.New(t)
	ss := statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(d.store),
	)

	d.logline = logline.New(t, logline.WithCaptureAll())
	d.workflow = workflow.New(t,
		workflow.WithNoDB(),
		workflow.WithSentryInstance(sen),
		workflow.WithDaprdOptions(0,
			daprd.WithAppID(uuid.New().String()),
			daprd.WithSocket(t, sock),
			daprd.WithExecOptions(exec.WithStdout(d.logline.Stdout()), exec.WithStderr(d.logline.Stderr())),
			daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mystore
spec:
  type: state.%s
  version: v1
  metadata:
  - name: actorStateStore
    value: "true"
`, ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(d.logline, sen, ss, d.workflow),
	}
}

func (d *drainedwakeup) Run(t *testing.T, ctx context.Context) {
	d.workflow.WaitUntilRunning(t, ctx)

	const wfID = "drainedwakeup-wf"

	r := d.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(task.ActivityContext) (any, error) {
		return nil, nil
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		var out string
		if err := octx.CallActivity("act").Await(&out); err != nil {
			return nil, err
		}
		return nil, octx.WaitForSingleEvent("finish", time.Hour).Await(nil)
	}))

	bc := d.workflow.BackendClient(t, ctx)
	_, err := d.workflow.GRPCClient(t, ctx).StartWorkflowBeta1(ctx, &rtv1.StartWorkflowRequest{
		WorkflowComponent: "dapr",
		WorkflowName:      "wf",
		InstanceId:        wfID,
	})
	require.NoError(t, err)

	// The activity result is consumed and the workflow parks on the event.
	scheduledID := int32(-1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		hist, herr := bc.GetInstanceHistory(ctx, api.InstanceID(wfID))
		if !assert.NoError(c, herr) {
			return
		}
		for _, he := range hist.GetEvents() {
			if tc := he.GetTaskCompleted(); tc != nil {
				scheduledID = tc.GetTaskScheduledId()
			}
		}
		assert.NotEqual(c, int32(-1), scheduledID)
	}, 15*time.Second, 50*time.Millisecond)

	// A redelivered result is a duplicate: it re-drives a wake-up that finds
	// the inbox empty.
	dup, err := proto.Marshal(&protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_TaskCompleted{TaskCompleted: &protos.TaskCompletedEvent{
			TaskScheduledId: scheduledID,
			Result:          wrapperspb.String(`"done"`),
		}},
	})
	require.NoError(t, err)
	wfActorType := fmt.Sprintf("dapr.internal.%s.%s.workflow", d.workflow.Dapr().Namespace(), d.workflow.Dapr().AppID())
	ignored := fmt.Sprintf("Workflow actor '%s': ignoring run request for reminder 'new-event-tc-%d'", wfID, scheduledID)

	historyReads := d.store.CountBulkGets(wfID + "||history-")
	const wakeups = 3
	for i := range wakeups {
		before := d.logline.Count(ignored)
		_, err = d.workflow.InternalGRPCClient(t, ctx).CallActor(ctx,
			internalv1pb.NewInternalInvokeRequest("AddWorkflowEvent").
				WithActor(wfActorType, wfID).
				WithData(dup),
		)
		require.NoError(t, err)
		require.Eventuallyf(t, func() bool {
			return d.logline.Count(ignored) > before
		}, 15*time.Second, 10*time.Millisecond, "wake-up %d did not fire", i)
	}

	assert.Zero(t, historyReads(), "drained wake-ups reloaded the workflow history from the store")

	_, err = d.workflow.GRPCClient(t, ctx).RaiseEventWorkflowBeta1(ctx, &rtv1.RaiseEventWorkflowRequest{
		WorkflowComponent: "dapr",
		InstanceId:        wfID,
		EventName:         "finish",
	})
	require.NoError(t, err)
	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
