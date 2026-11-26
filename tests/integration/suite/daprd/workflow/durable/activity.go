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

package durable

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/dapr/pkg/runtime/wfengine/todo"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(activity))
}

// activity holds every activity result publish to the parent workflow. A
// CompleteActivityTask call without the durable header is answered before
// the publish, as before. With the header it is answered only once the
// result is published.
type activity struct {
	workflow *workflow.Workflow
}

const activityPublishHold = 3 * time.Second

func (r *activity) Setup(t *testing.T) []framework.Option {
	r.workflow = workflow.New(t,
		workflow.WithClusteredDeployment(false),
		workflow.WithDaprdOptions(0, daprd.WithExecOptions(exec.WithEnvVars(t, "DAPR_WORKFLOW_TEST_ACTIVITY_PUBLISH_HOLD", activityPublishHold.String()))),
	)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *activity) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)
	worker := r.workflow.ConnectRawWorker(t, ctx)
	thub := worker.Client()
	cl := r.workflow.ManagementClient(t, ctx)

	// The workflow schedules two activities and completes once both results
	// are in its history. Its turns are answered on their own goroutine, so
	// that a result publish waiting for a turn never waits for this test.
	activities := make(chan *protos.WorkItem, 2)
	go func() {
		for wi := range worker.Items() {
			if wi.GetActivityRequest() != nil {
				activities <- wi
				continue
			}
			wr := wi.GetWorkflowRequest()
			resp := &protos.WorkflowResponse{InstanceId: wr.GetInstanceId(), CompletionToken: wi.GetCompletionToken()}
			var scheduled, completed int
			for _, e := range append(wr.GetPastEvents(), wr.GetNewEvents()...) {
				if e.GetTaskScheduled() != nil {
					scheduled++
				}
				if e.GetTaskCompleted() != nil {
					completed++
				}
			}
			switch {
			case scheduled == 0:
				for id := range int32(2) {
					resp.Actions = append(resp.Actions, &protos.WorkflowAction{
						Id:                 id,
						WorkflowActionType: &protos.WorkflowAction_ScheduleTask{ScheduleTask: &protos.ScheduleTaskAction{Name: "act"}},
					})
				}
			case completed == 2:
				resp = workflow.CompletedResponse(wi)
			}
			//nolint:errcheck
			thub.CompleteWorkflowTask(ctx, resp)
		}
	}()

	id := workflow.StartWorkflow(t, ctx, cl, "durable-activity")

	nextActivity := func() *protos.ActivityResponse {
		t.Helper()
		select {
		case wi := <-activities:
			ar := wi.GetActivityRequest()
			return &protos.ActivityResponse{
				InstanceId:      ar.GetWorkflowInstance().GetInstanceId(),
				TaskId:          ar.GetTaskId(),
				Result:          wrapperspb.String(`"ok"`),
				CompletionToken: wi.GetCompletionToken(),
			}
		case <-time.After(30 * time.Second):
			require.FailNow(t, "timed out waiting for an activity work item")
			return nil
		}
	}

	start := time.Now()
	_, err := thub.CompleteActivityTask(ctx, nextActivity())
	require.NoError(t, err)
	assert.Less(t, time.Since(start), activityPublishHold, "without the header the answer does not wait for the publish")

	var header metadata.MD
	start = time.Now()
	_, err = thub.CompleteActivityTask(metadata.AppendToOutgoingContext(ctx, todo.DurableHeader, "true"), nextActivity(), grpc.Header(&header))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, time.Since(start), activityPublishHold, "with the header the answer waits for the publish")
	assert.Equal(t, []string{"true"}, header.Get(todo.DurableHeader))

	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	t.Cleanup(cancel)
	meta, err := cl.WaitForWorkflowCompletion(waitCtx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
}
