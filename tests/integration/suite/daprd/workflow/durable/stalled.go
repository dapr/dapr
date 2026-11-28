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
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

func init() {
	suite.Register(new(stalled))
}

// stalled answers a workflow's second turn with patches that do not match
// its history, which stalls the workflow. daprd holds a stalled turn until
// the stall is released, but the worker's CompleteWorkflowTask call must be
// answered once the stall is saved.
type stalled struct {
	workflow *workflow.Workflow
}

func (s *stalled) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t, workflow.WithClusteredDeployment(false))

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *stalled) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)
	worker := s.workflow.ConnectRawWorker(t, ctx)
	cl := s.workflow.ManagementClient(t, ctx)
	id := workflow.StartWorkflow(t, ctx, cl, "durable-stalled")

	// The first turn records patch "a" and waits on a timer.
	wi := worker.NextWorkflowItem(t)
	_, err := worker.Client().CompleteWorkflowTask(ctx, &protos.WorkflowResponse{
		InstanceId:      wi.GetWorkflowRequest().GetInstanceId(),
		CompletionToken: wi.GetCompletionToken(),
		Version:         &protos.WorkflowVersion{Patches: []string{"a"}},
		Actions: []*protos.WorkflowAction{{
			Id: 0,
			WorkflowActionType: &protos.WorkflowAction_CreateTimer{
				CreateTimer: &protos.CreateTimerAction{FireAt: timestamppb.New(time.Now().Add(time.Second))},
			},
		}},
	})
	require.NoError(t, err)

	// The second turn reports patch "b" instead, which stalls the workflow.
	resp := workflow.CompletedResponse(worker.NextWorkflowItem(t))
	resp.Version = &protos.WorkflowVersion{Patches: []string{"b"}}
	errCh := make(chan error, 1)
	go func() {
		_, cerr := worker.Client().CompleteWorkflowTask(ctx, resp)
		errCh <- cerr
	}()
	select {
	case cerr := <-errCh:
		require.NoError(t, cerr)
	case <-time.After(10 * time.Second):
		require.FailNow(t, "the completion of a stalled turn was not answered")
	}

	meta, err := cl.FetchWorkflowMetadata(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED, meta.GetRuntimeStatus())
}
