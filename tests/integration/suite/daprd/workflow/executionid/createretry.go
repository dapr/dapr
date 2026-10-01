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
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(createretry))
}

// A create retried with the same ExecutionId after the workflow completed,
// ex: a lost response, must answer success without running the workflow again.
type createretry struct {
	workflow *workflow.Workflow
}

func (c *createretry) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *createretry) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	const wfID = "createretry-wf"

	var runs atomic.Int64
	r := c.workflow.Registry()
	require.NoError(t, r.AddActivityN("act", func(task.ActivityContext) (any, error) {
		runs.Add(1)
		return nil, nil
	}))
	require.NoError(t, r.AddWorkflowN("wf", func(octx *task.WorkflowContext) (any, error) {
		return nil, octx.CallActivity("act").Await(nil)
	}))

	bc := c.workflow.BackendClient(t, ctx)
	gclient := c.workflow.GRPCClient(t, ctx)

	createBytes, err := proto.Marshal(&protos.CreateWorkflowInstanceRequest{
		StartEvent: &protos.HistoryEvent{
			EventId:   -1,
			Timestamp: timestamppb.Now(),
			EventType: &protos.HistoryEvent_ExecutionStarted{
				ExecutionStarted: &protos.ExecutionStartedEvent{
					Name: "wf",
					WorkflowInstance: &protos.WorkflowInstance{
						InstanceId:  wfID,
						ExecutionId: wrapperspb.String(uuid.New().String()),
					},
				},
			},
		},
	})
	require.NoError(t, err)

	create := &rtv1.InvokeActorRequest{
		ActorType: fmt.Sprintf("dapr.internal.%s.%s.workflow", c.workflow.Dapr().Namespace(), c.workflow.Dapr().AppID()),
		ActorId:   wfID,
		Method:    "CreateWorkflowInstance",
		Data:      createBytes,
	}

	assert.EventuallyWithT(t, func(co *assert.CollectT) {
		_, ierr := gclient.InvokeActor(ctx, create)
		assert.NoError(co, ierr)
	}, 5*time.Second, 50*time.Millisecond)

	meta, err := bc.WaitForWorkflowCompletion(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	require.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus())
	require.Equal(t, int64(1), runs.Load())

	hist, err := bc.GetInstanceHistory(ctx, api.InstanceID(wfID))
	require.NoError(t, err)

	_, err = gclient.InvokeActor(ctx, create)
	require.NoError(t, err, "a retry of the committed create must answer success")

	assert.Never(t, func() bool {
		meta, ferr := bc.FetchWorkflowMetadata(ctx, api.InstanceID(wfID))
		return ferr != nil ||
			meta.GetRuntimeStatus() != protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED ||
			runs.Load() != 1
	}, 5*time.Second, 50*time.Millisecond, "a retry of the committed create must not run the workflow again")

	after, err := bc.GetInstanceHistory(ctx, api.InstanceID(wfID))
	require.NoError(t, err)
	assert.Len(t, after.GetEvents(), len(hist.GetEvents()), "the completed history must not be replaced")
}
