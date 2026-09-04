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

package terminate

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(enforced))
}

// enforced verifies that the runtime itself guarantees a terminate delivered
// mid-batch, independent of SDK behavior. The worker here speaks the raw task
// hub protocol and answers every workflow request with a CreateTimer action,
// ignoring the ExecutionTerminated event in its batch. This mimics the
// misbehaving SDK that motivated the fix; the SDK-based tests in the batched
// subpackage cannot cover this path because a correct SDK converts the
// terminate into a completion action before the runtime's enforcement is
// needed.
type enforced struct {
	workflow *workflow.Workflow
}

func (e *enforced) Setup(t *testing.T) []framework.Option {
	e.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(e.workflow),
	}
}

func (e *enforced) Run(t *testing.T, ctx context.Context) {
	e.workflow.WaitUntilRunning(t, ctx)

	// A worker that parks every workflow on a timer, forever, no matter what
	// events it is sent. Reconnects across the daprd restart below.
	var timerSeq atomic.Int32
	wctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go func() {
		for wctx.Err() == nil {
			conn, err := grpc.NewClient(e.workflow.Dapr().GRPCAddress(),
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if err != nil {
				time.Sleep(time.Millisecond * 50)
				continue
			}
			sc := protos.NewTaskHubSidecarServiceClient(conn)
			if _, err = sc.Hello(wctx, new(emptypb.Empty)); err != nil {
				conn.Close()
				time.Sleep(time.Millisecond * 50)
				continue
			}
			stream, err := sc.GetWorkItems(wctx, new(protos.GetWorkItemsRequest))
			if err != nil {
				conn.Close()
				time.Sleep(time.Millisecond * 50)
				continue
			}
			for {
				wi, rerr := stream.Recv()
				if rerr != nil {
					break
				}
				wr := wi.GetWorkflowRequest()
				if wr == nil {
					continue
				}
				_, rerr = sc.CompleteWorkflowTask(wctx, &protos.WorkflowResponse{
					InstanceId: wr.GetInstanceId(),
					Actions: []*protos.WorkflowAction{{
						Id: timerSeq.Add(1) - 1,
						WorkflowActionType: &protos.WorkflowAction_CreateTimer{
							CreateTimer: &protos.CreateTimerAction{
								FireAt: timestamppb.New(time.Now().Add(time.Second * 5)),
							},
						},
					}},
				})
				if rerr != nil {
					break
				}
			}
			conn.Close()
		}
	}()

	cl := e.workflow.ManagementClient(t, ctx)
	id, err := cl.ScheduleNewWorkflow(ctx, "terminate-enforced", api.WithInstanceID("terminate-enforced"))
	require.NoError(t, err)
	_, err = cl.WaitForWorkflowStart(ctx, id)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, fworkflow.CountHistoryEventsMatching(t, ctx, cl, id, func(ev *protos.HistoryEvent) bool {
			return ev.GetTimerCreated() != nil
		}))
	}, time.Second*20, time.Millisecond*10)

	// Persist the terminate directly into the inbox without a wake-up reminder.
	// When the pending timer fires it appends its TimerFired behind the
	// terminate and the worker receives [ExecutionTerminated, TimerFired], which
	// it answers with another timer as if the terminate did not exist.
	fworkflow.InjectInboxEvent(t, ctx, e.workflow.DB(), e.workflow.Dapr(), string(id), &protos.HistoryEvent{
		EventId:   -1,
		Timestamp: timestamppb.Now(),
		EventType: &protos.HistoryEvent_ExecutionTerminated{
			ExecutionTerminated: &protos.ExecutionTerminatedEvent{
				Input: wrapperspb.String(`"stop"`),
			},
		},
	})

	e.workflow.Dapr().Restart(t, ctx)
	e.workflow.Dapr().WaitUntilRunning(t, ctx)
	cl = e.workflow.ManagementClient(t, ctx)

	meta, err := cl.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, api.RUNTIME_STATUS_TERMINATED.String(), meta.GetRuntimeStatus().String())
	require.Equal(t, `"stop"`, meta.GetOutput().GetValue())
}
