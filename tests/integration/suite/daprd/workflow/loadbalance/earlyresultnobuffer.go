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
	"google.golang.org/protobuf/types/known/timestamppb"
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
	suite.Register(new(earlytimernobuffer))
	suite.Register(new(earlychildnobuffer))
}

// earlyresultnobuffer, earlytimernobuffer and earlychildnobuffer run the
// earlyresult fault sequence against a worker that does not buffer an early
// resolution. Like the Python, Java and JavaScript SDKs, it replays the
// events in order and ignores a resolution with no pending activity, timer
// or child workflow, and it gives every new scheduling a new
// TaskExecutionId. The workflow runs step0, then a middle step (an
// activity, a zero-length timer or a child workflow), then step2; the turn
// that schedules the middle step fails its save after the step was started,
// so its resolution reaches the inbox before its scheduling is saved. The
// workflow must still complete.
type (
	earlyresultnobuffer struct{ nobuffer }
	earlytimernobuffer  struct{ nobuffer }
	earlychildnobuffer  struct{ nobuffer }
)

type stepKind int

const (
	kindActivity stepKind = iota
	kindTimer
	kindChild
)

type nobuffer struct {
	id       string
	middle   stepKind
	workflow *workflow.Workflow
	ss       *statestore.StateStore
	store    *fault.Store
}

func (e *earlyresultnobuffer) Setup(t *testing.T) []framework.Option {
	return e.setup(t, "earlyresultnobuffer", kindActivity)
}

func (e *earlytimernobuffer) Setup(t *testing.T) []framework.Option {
	return e.setup(t, "earlytimernobuffer", kindTimer)
}

func (e *earlychildnobuffer) Setup(t *testing.T) []framework.Option {
	return e.setup(t, "earlychildnobuffer", kindChild)
}

func (n *nobuffer) setup(t *testing.T, id string, middle stepKind) []framework.Option {
	os.SkipWindows(t)

	n.id, n.middle = id, middle

	n.store = fault.New(t)
	sock := socket.New(t)
	n.ss = statestore.New(t,
		statestore.WithSocket(sock),
		statestore.WithStateStore(n.store),
	)

	n.workflow = workflow.New(t,
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
`, n.ss.SocketName())),
		),
	)

	return []framework.Option{
		framework.WithProcesses(n.ss, n.workflow),
	}
}

// replay returns the actions of the workflow step0, middle, step2, which
// returns "done", the way an SDK without an early-resolution buffer does:
// events are applied in order, a resolution with no pending step is ignored,
// and the actions are the schedulings no event in the history consumed. A
// child workflow, any instance other than parent, completes at once.
func replay(parent string, middle stepKind, instance string, events []*protos.HistoryEvent) []*protos.WorkflowAction {
	if instance != parent {
		return []*protos.WorkflowAction{completeAction(0, `"child"`)}
	}
	var started time.Time
	pending := map[int32]bool{}
	actions := map[int32]*protos.WorkflowAction{}
	schedule := func(id int32, kind stepKind, name string) {
		pending[id] = true
		var a *protos.WorkflowAction
		switch kind {
		case kindTimer:
			a = &protos.WorkflowAction{Id: id, WorkflowActionType: &protos.WorkflowAction_CreateTimer{
				CreateTimer: &protos.CreateTimerAction{FireAt: timestamppb.New(started)},
			}}
		case kindChild:
			a = &protos.WorkflowAction{Id: id, WorkflowActionType: &protos.WorkflowAction_CreateChildWorkflow{
				CreateChildWorkflow: &protos.CreateChildWorkflowAction{InstanceId: parent + "-child", Name: "child"},
			}}
		default:
			a = &protos.WorkflowAction{Id: id, WorkflowActionType: &protos.WorkflowAction_ScheduleTask{
				ScheduleTask: &protos.ScheduleTaskAction{Name: name, TaskExecutionId: uuid.NewString()},
			}}
		}
		actions[id] = a
	}
	resolve := func(id int32) {
		if !pending[id] {
			return
		}
		delete(pending, id)
		switch id {
		case 0:
			schedule(1, middle, "step1")
		case 1:
			schedule(2, kindActivity, "step2")
		case 2:
			actions[3] = completeAction(3, `"done"`)
		}
	}
	for _, ev := range events {
		switch {
		case ev.GetExecutionStarted() != nil:
			started = ev.GetTimestamp().AsTime()
			schedule(0, kindActivity, "step0")
		case ev.GetTaskScheduled() != nil, ev.GetTimerCreated() != nil, ev.GetChildWorkflowInstanceCreated() != nil:
			delete(actions, ev.GetEventId())
		case ev.GetTaskCompleted() != nil:
			resolve(ev.GetTaskCompleted().GetTaskScheduledId())
		case ev.GetTimerFired() != nil:
			resolve(ev.GetTimerFired().GetTimerId())
		case ev.GetChildWorkflowInstanceCompleted() != nil:
			resolve(ev.GetChildWorkflowInstanceCompleted().GetTaskScheduledId())
		}
	}
	list := make([]*protos.WorkflowAction, 0, len(actions))
	for _, id := range slices.Sorted(maps.Keys(actions)) {
		list = append(list, actions[id])
	}
	return list
}

func completeAction(id int32, result string) *protos.WorkflowAction {
	return &protos.WorkflowAction{
		Id: id,
		WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
			CompleteWorkflow: &protos.CompleteWorkflowAction{
				WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
				Result:         wrapperspb.String(result),
			},
		},
	}
}

func (n *nobuffer) Run(t *testing.T, ctx context.Context) {
	n.workflow.WaitUntilRunning(t, ctx)

	conn := n.workflow.Dapr().GRPCConn(t, ctx)
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
					Actions:         replay(n.id, n.middle, wr.GetInstanceId(), append(wr.GetPastEvents(), wr.GetNewEvents()...)),
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
	n.store.SetMultiObserver(func(req *state.TransactionalStateRequest) {
		var history, inboxDelete bool
		for _, op := range req.Operations {
			switch v := op.(type) {
			case state.SetRequest:
				history = history || strings.Contains(v.Key, n.id+"||history-")
			case state.DeleteRequest:
				inboxDelete = inboxDelete || strings.Contains(v.Key, n.id+"||inbox-")
			}
		}
		switch {
		case history && historySaves.Add(1) == 2:
			n.store.ArmFailures(n.id+"||history-", 1, failed)
		case inboxDelete && !history:
			n.store.ArmFailures(n.id+"||inbox-", 1, nil)
		}
	})

	sched := client.NewTaskHubGrpcClient(conn, logger.New(t))
	_, err = sched.ScheduleNewWorkflow(ctx, "seq", api.WithInstanceID(api.InstanceID(n.id)))
	require.NoError(t, err)
	select {
	case <-failed:
	case <-time.After(20 * time.Second):
		require.Fail(t, "the turn scheduling the middle step never saved")
	}

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	meta, err := sched.WaitForWorkflowCompletion(waitCtx, api.InstanceID(n.id))
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus(), meta.GetFailureDetails().GetErrorMessage())
	assert.JSONEq(t, `"done"`, meta.GetOutput().GetValue())
}
