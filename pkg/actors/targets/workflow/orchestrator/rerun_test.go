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

package orchestrator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	routerfake "github.com/dapr/dapr/pkg/actors/router/fake"
	internalsv1pb "github.com/dapr/dapr/pkg/proto/internals/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/backend/runtimestate"
	"github.com/dapr/kit/logger"
)

func TestRerunChildWorkflow(t *testing.T) {
	for _, tc := range []struct {
		name     string
		parent   bool
		status   protos.OrchestrationStatus
		eventID  uint32
		wantCode codes.Code
	}{
		{"completed child", true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, 0, codes.OK},
		{"failed child", true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, 0, codes.OK},
		{"running child", true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING, 0, codes.InvalidArgument},
		{"missing child event", true, protos.OrchestrationStatus_ORCHESTRATION_STATUS_FAILED, 99, codes.NotFound},
		{"completed root", false, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, 0, codes.OK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const sourceID = "parent:0000"
			h := newWakeHarness(t, sourceID, true)
			h.fact.resiliency = resiliency.New(logger.NewLogger("test"))
			var forked *protos.BackendWorkflowState
			h.fact.router = routerfake.New().WithCallFn(func(_ context.Context, req *internalsv1pb.InternalInvokeRequest) (*internalsv1pb.InternalInvokeResponse, error) {
				assert.Equal(t, todo.RerunWorkflowInstance, req.GetMessage().GetMethod())
				assert.Equal(t, "rerun", req.GetActor().GetActorId())
				forked = new(protos.BackendWorkflowState)
				require.NoError(t, proto.Unmarshal(req.GetMessage().GetData().GetValue(), forked))
				return &internalsv1pb.InternalInvokeResponse{Status: &internalsv1pb.Status{Code: 200}}, nil
			})
			h.primeRunning(t, sourceID, 0)
			started := h.orch.state.History[0].GetExecutionStarted()
			if tc.parent {
				started.ParentInstance = &protos.ParentInstanceInfo{
					TaskScheduledId:  7,
					WorkflowInstance: &protos.WorkflowInstance{InstanceId: "parent"},
				}
			}
			originalStart := proto.Clone(started)
			if tc.status != protos.OrchestrationStatus_ORCHESTRATION_STATUS_RUNNING {
				h.orch.state.AddToHistory(&protos.HistoryEvent{EventId: -1, EventType: &protos.HistoryEvent_ExecutionCompleted{
					ExecutionCompleted: &protos.ExecutionCompletedEvent{WorkflowStatus: tc.status},
				}})
			}
			h.orch.rstate = runtimestate.NewWorkflowRuntimeState(sourceID, nil, h.orch.state.History)
			h.orch.ometa = h.orch.ometaFromState(h.orch.rstate, started)
			if tc.parent {
				require.Equal(t, "parent", h.orch.ometa.ParentInstanceId)
			}
			h.persist(t)
			data, err := proto.Marshal(&backend.RerunWorkflowFromEventRequest{
				SourceInstanceID: sourceID, NewInstanceID: new("rerun"), EventID: tc.eventID,
			})
			require.NoError(t, err)
			_, err = h.orch.handleInvoke(t.Context(), internalsv1pb.NewInternalInvokeRequest(todo.ForkWorkflowHistory).WithData(data))
			require.Equal(t, tc.wantCode, status.Code(err), "rerun returned %v", err)
			require.True(t, proto.Equal(originalStart, started), "fork must preserve the source's parent link")
			if tc.wantCode != codes.OK {
				require.Nil(t, forked)
				return
			}
			require.NotNil(t, forked)
			require.NotEmpty(t, forked.GetHistory())
			rerunStart := forked.GetHistory()[0].GetExecutionStarted()
			require.NotNil(t, rerunStart)
			assert.Nil(t, rerunStart.GetParentInstance(), "rerun must not notify the original parent")
			assert.Equal(t, started.GetName(), rerunStart.GetName())
			assert.True(t, proto.Equal(started.GetInput(), rerunStart.GetInput()))
			require.Len(t, forked.GetInbox(), 1)
			activity := forked.GetInbox()[0].GetTaskScheduled()
			require.NotNil(t, activity)
			assert.Equal(t, "act", activity.GetName())
			assert.Equal(t, sourceID, activity.GetRerunParentInstanceInfo().GetInstanceID())
		})
	}
}
