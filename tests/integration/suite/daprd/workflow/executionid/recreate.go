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
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
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
	suite.Register(new(recreate))
}

// A completed workflow recreated under the same instance ID must execute its
// activities again and complete.
type recreate struct {
	workflow *workflow.Workflow
}

func (r *recreate) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *recreate) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	const (
		instanceID   = "recreate-executionid"
		workflowName = "recreater"
		activityName = "once-per-run"
	)

	deriveTaskExecutionID := func(executionID, instanceID string, taskID int32, name string) string {
		seed := executionID
		if seed == "" {
			seed = instanceID
		}
		sum := sha256.Sum256([]byte(seed + "|" + instanceID + "|activity|" + strconv.Itoa(int(taskID)) + "|" + name))
		return hex.EncodeToString(sum[:16])
	}

	var activityRuns atomic.Int64

	conn := r.workflow.Dapr().GRPCConn(t, ctx)
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

				completed := false
				for _, e := range append(wr.GetPastEvents(), wr.GetNewEvents()...) {
					if e.GetTaskCompleted() != nil {
						completed = true
						break
					}
				}

				resp := &protos.WorkflowResponse{
					InstanceId:      wr.GetInstanceId(),
					CompletionToken: wi.GetCompletionToken(),
				}
				if completed {
					resp.Actions = []*protos.WorkflowAction{{
						Id: 1,
						WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
							CompleteWorkflow: &protos.CompleteWorkflowAction{
								WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
								Result:         wrapperspb.String(`"done"`),
							},
						},
					}}
				} else {
					resp.Actions = []*protos.WorkflowAction{{
						Id: 0,
						WorkflowActionType: &protos.WorkflowAction_ScheduleTask{
							ScheduleTask: &protos.ScheduleTaskAction{
								Name: activityName,
								TaskExecutionId: deriveTaskExecutionID(
									wr.GetExecutionId().GetValue(),
									wr.GetInstanceId(), 0, activityName),
							},
						},
					}}
				}

				//nolint:errcheck
				thub.CompleteWorkflowTask(ctx, resp)

			case *protos.WorkItem_ActivityRequest:
				ar := req.ActivityRequest
				activityRuns.Add(1)

				//nolint:errcheck
				thub.CompleteActivityTask(ctx, &protos.ActivityResponse{
					InstanceId:      ar.GetWorkflowInstance().GetInstanceId(),
					TaskId:          ar.GetTaskId(),
					Result:          wrapperspb.String(`"ok"`),
					CompletionToken: wi.GetCompletionToken(),
				})
			}
		}
	}()

	sched := client.NewTaskHubGrpcClient(conn, logger.New(t))

	id, err := sched.ScheduleNewWorkflow(ctx, workflowName, api.WithInstanceID(instanceID))
	require.NoError(t, err)

	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancel)
	meta, err := sched.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.Equal(t, int64(1), activityRuns.Load())

	id, err = sched.ScheduleNewWorkflow(ctx, workflowName, api.WithInstanceID(instanceID))
	require.NoError(t, err)

	waitCtx2, cancel2 := context.WithTimeout(ctx, 60*time.Second)
	t.Cleanup(cancel2)
	meta, err = sched.WaitForWorkflowCompletion(waitCtx2, id)
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	assert.Equal(t, int64(2), activityRuns.Load())
}
