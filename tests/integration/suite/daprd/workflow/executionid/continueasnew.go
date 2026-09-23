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

package executionid

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

func init() {
	suite.Register(new(continueasnew))
}

type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const (
		instanceID   = "can-executionid"
		workflowName = "canner"
	)

	type turn struct {
		input  string
		execID string
	}
	var lock sync.Mutex
	turns := make([]turn, 0, 4)

	conn := c.workflow.Dapr().GRPCConn(t, ctx)
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

			req, ok := wi.GetRequest().(*protos.WorkItem_WorkflowRequest)
			if !ok {
				continue
			}
			wr := req.WorkflowRequest

			var input string
			timerFired := false
			for _, e := range append(wr.GetPastEvents(), wr.GetNewEvents()...) {
				if es := e.GetExecutionStarted(); es != nil {
					input = es.GetInput().GetValue()
				}
				if e.GetTimerFired() != nil {
					timerFired = true
				}
			}

			lock.Lock()
			turns = append(turns, turn{input: input, execID: wr.GetExecutionId().GetValue()})
			lock.Unlock()

			resp := &protos.WorkflowResponse{
				InstanceId:      wr.GetInstanceId(),
				CompletionToken: wi.GetCompletionToken(),
			}
			switch {
			case input == `"gen0"` && !timerFired:
				resp.Actions = []*protos.WorkflowAction{{
					Id: 0,
					WorkflowActionType: &protos.WorkflowAction_CreateTimer{
						CreateTimer: &protos.CreateTimerAction{
							FireAt: timestamppb.New(time.Now().Add(500 * time.Millisecond)),
						},
					},
				}}
			case input == `"gen0"` && timerFired:
				resp.Actions = []*protos.WorkflowAction{{
					Id: 1,
					WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
						CompleteWorkflow: &protos.CompleteWorkflowAction{
							WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_CONTINUED_AS_NEW,
							Result:         wrapperspb.String(`"gen1"`),
						},
					},
				}}
			default:
				resp.Actions = []*protos.WorkflowAction{{
					Id: 0,
					WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
						CompleteWorkflow: &protos.CompleteWorkflowAction{
							WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
							Result:         wrapperspb.String(`"done"`),
						},
					},
				}}
			}

			//nolint:errcheck
			thub.CompleteWorkflowTask(ctx, resp)
		}
	}()

	sched := client.NewTaskHubGrpcClient(conn, logger.New(t))

	id, err := sched.ScheduleNewWorkflow(ctx, workflowName,
		api.WithInstanceID(instanceID),
		api.WithInput("gen0"),
	)
	require.NoError(t, err)

	meta, err := sched.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())

	lock.Lock()
	seen := append([]turn(nil), turns...)
	lock.Unlock()

	require.GreaterOrEqual(t, len(seen), 3,
		"expected at least three workflow turns: generation 0 start, generation 0 timer fired, generation 1 start")

	gens := make(map[string]map[string]struct{})
	for i, tr := range seen {
		assert.NotEmpty(t, tr.execID,
			"turn %d (input %s): every workflow work item must carry the execution ID; SDKs seed deterministic values (NewGuid, TaskExecutionId, child instance IDs) from it and fall back to the instance ID when it is empty, which repeats those values across ContinueAsNew generations", i, tr.input)
		if gens[tr.input] == nil {
			gens[tr.input] = make(map[string]struct{})
		}
		gens[tr.input][tr.execID] = struct{}{}
	}

	require.Len(t, gens[`"gen0"`], 1,
		"the execution ID must be stable across turns and replays within generation 0: %v", gens[`"gen0"`])
	require.Len(t, gens[`"gen1"`], 1,
		"the execution ID must be stable across turns and replays within generation 1: %v", gens[`"gen1"`])
	assert.NotEqual(t, gens[`"gen0"`], gens[`"gen1"`],
		"ContinueAsNew must mint a fresh execution ID so SDK-side deterministic values differ per generation")
}
