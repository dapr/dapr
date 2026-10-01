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
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore"
	"github.com/dapr/dapr/tests/integration/framework/process/statestore/fault"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(earlyresultnobuffer))
}

// earlyresultnobuffer is earlyresult with a worker that does not buffer an
// early result. Like the Python, Java and JavaScript SDKs, it replays the
// events in order and ignores a TaskCompleted with no pending task, and it
// gives every new scheduling a new TaskExecutionId. The workflow must still
// complete with step1's result.
type earlyresultnobuffer struct {
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (e *earlyresultnobuffer) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	e.store = fault.New(t)
	sock := socket.New(t)
	e.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(e.store),
	)

	e.workflow = workflow.New(t,
		// As in earlyresult: under history signing the retried completion
		// would read as tampering instead of exercising the early-result path.
		workflow.WithSigningDisabledN(0),
		workflow.WithNoDB(),
		workflow.WithFastPath(true),
		workflow.WithDaprdOptions(0,
			daprd.WithSocket(t, sock),
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
`, e.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(e.ss, e.workflow),
	}
}

// sequence replays the workflow step0, step1, step2, returning step1's
// result, the way an SDK without an early-result buffer does: events are
// applied in order, a resolution with no pending task is ignored, and the
// actions are the schedulings that no TaskScheduled in the events consumed.
func sequence(events []*protos.HistoryEvent) []*protos.WorkflowAction {
	steps := [...]string{"step0", "step1", "step2"}
	pending := map[int32]bool{}
	actions := map[int32]*protos.WorkflowAction{}
	var out string
	schedule := func(id int32) {
		pending[id] = true
		actions[id] = &protos.WorkflowAction{
			Id: id,
			WorkflowActionType: &protos.WorkflowAction_ScheduleTask{
				ScheduleTask: &protos.ScheduleTaskAction{Name: steps[id], TaskExecutionId: uuid.NewString()},
			},
		}
	}
	for _, ev := range events {
		switch {
		case ev.GetExecutionStarted() != nil:
			schedule(0)
		case ev.GetTaskScheduled() != nil:
			delete(actions, ev.GetEventId())
		case ev.GetTaskCompleted() != nil:
			id := ev.GetTaskCompleted().GetTaskScheduledId()
			if !pending[id] {
				continue
			}
			delete(pending, id)
			if id == 1 {
				out = ev.GetTaskCompleted().GetResult().GetValue()
			}
			next := id + 1
			if next < int32(len(steps)) {
				schedule(next)
				continue
			}
			actions[next] = &protos.WorkflowAction{
				Id: next,
				WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
					CompleteWorkflow: &protos.CompleteWorkflowAction{
						WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
						Result:         wrapperspb.String(out),
					},
				},
			}
		}
	}
	list := make([]*protos.WorkflowAction, 0, len(actions))
	for _, id := range slices.Sorted(maps.Keys(actions)) {
		list = append(list, actions[id])
	}
	return list
}

func (e *earlyresultnobuffer) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	const id = "earlyresultnobuffer"

	conn := e.workflow.Dapr().GRPCConn(t, ctx)
	thub := protos.NewTaskHubSidecarServiceClient(conn)
	_, err := thub.Hello(ctx, new(emptypb.Empty))
	require.NoError(t, err)
	stream, err := thub.GetWorkItems(ctx, new(protos.GetWorkItemsRequest))
	require.NoError(t, err)

	go func() {
		for {
			wi, rerr := stream.Recv()
			if rerr != nil {
				return
			}
			switch req := wi.GetRequest().(type) {
			case *protos.WorkItem_WorkflowRequest:
				wr := req.WorkflowRequest
				//nolint:errcheck
				thub.CompleteWorkflowTask(ctx, &protos.WorkflowResponse{
					InstanceId:      wr.GetInstanceId(),
					CompletionToken: wi.GetCompletionToken(),
					Actions:         sequence(append(wr.GetPastEvents(), wr.GetNewEvents()...)),
				})
			case *protos.WorkItem_ActivityRequest:
				ar := req.ActivityRequest
				//nolint:errcheck
				thub.CompleteActivityTask(ctx, &protos.ActivityResponse{
					InstanceId:      ar.GetWorkflowInstance().GetInstanceId(),
					TaskId:          ar.GetTaskId(),
					Result:          wrapperspb.String(`"` + ar.GetName() + `"`),
					CompletionToken: wi.GetCompletionToken(),
				})
			}
		}
	}()

	var historySaves atomic.Int32
	failed := make(chan struct{})
	e.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		var history, inboxDelete bool
		for _, op := range req.Operations {
			switch v := op.(type) {
			case state.SetRequest:
				history = history || strings.Contains(v.Key, id+"||history-")
			case state.DeleteRequest:
				inboxDelete = inboxDelete || strings.Contains(v.Key, id+"||inbox-")
			}
		}
		switch {
		case history && historySaves.Add(1) == 2:
			e.store.ArmFailures(id+"||history-", 1, failed)
		case inboxDelete && !history:
			e.store.ArmFailures(id+"||inbox-", 1, nil)
		}
	})

	sched := client.NewTaskHubGrpcClient(conn, logger.New(t))
	_, err = sched.ScheduleNewWorkflow(ctx, "seq", api.WithInstanceID(id))
	require.NoError(t, err)
	select {
	case <-failed:
	case <-time.After(20 * time.Second):
		require.Fail(t, "the turn dispatching step1 never saved")
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := sched.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), meta.GetFailureDetails().GetErrorMessage())
	assert.JSONEq(t, `"step1"`, meta.GetOutput().GetValue())
}
