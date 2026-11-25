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
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

// workItems receives the work items of stream until it ends, skipping health
// pings.
func workItems(stream protos.TaskHubSidecarService_GetWorkItemsClient) <-chan *protos.WorkItem {
	items := make(chan *protos.WorkItem, 8)
	go func() {
		defer close(items)
		for {
			wi, err := stream.Recv()
			if err != nil {
				return
			}
			if wi.GetHealthPing() == nil {
				items <- wi
			}
		}
	}()
	return items
}

// nextWorkflowItem returns the next workflow work item.
func nextWorkflowItem(t *testing.T, items <-chan *protos.WorkItem) *protos.WorkItem {
	t.Helper()
	select {
	case wi, ok := <-items:
		require.True(t, ok, "the work item stream ended")
		require.NotNil(t, wi.GetWorkflowRequest(), "expected a workflow work item, got %v", wi)
		return wi
	case <-time.After(30 * time.Second):
		require.FailNow(t, "timed out waiting for a workflow work item")
		return nil
	}
}

// completeResponse completes the workflow of wi with a result.
func completeResponse(wi *protos.WorkItem) *protos.WorkflowResponse {
	return &protos.WorkflowResponse{
		InstanceId:      wi.GetWorkflowRequest().GetInstanceId(),
		CompletionToken: wi.GetCompletionToken(),
		Actions: []*protos.WorkflowAction{{
			Id: 0,
			WorkflowActionType: &protos.WorkflowAction_CompleteWorkflow{
				CompleteWorkflow: &protos.CompleteWorkflowAction{
					WorkflowStatus: protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED,
					Result:         wrapperspb.String(`"done"`),
				},
			},
		}},
	}
}

// openStream connects a worker and returns the work items it receives.
func openStream(t *testing.T, ctx context.Context, thub protos.TaskHubSidecarServiceClient) <-chan *protos.WorkItem {
	t.Helper()
	stream, err := thub.GetWorkItems(ctx, new(protos.GetWorkItemsRequest))
	require.NoError(t, err)
	return workItems(stream)
}

// schedule starts a workflow without waiting for the answer: daprd answers
// once the first turn is saved, and the test drives that turn.
func schedule(t *testing.T, ctx context.Context, cl *client.TaskHubGrpcClient, name string) api.InstanceID {
	t.Helper()
	errCh := make(chan error, 1)
	go func() {
		_, err := cl.ScheduleNewWorkflow(ctx, name, api.WithInstanceID(api.InstanceID(name)))
		errCh <- err
	}()
	t.Cleanup(func() {
		select {
		case err := <-errCh:
			assert.NoError(t, err)
		case <-time.After(10 * time.Second):
			assert.Fail(t, "the workflow start was not answered")
		}
	})
	return api.InstanceID(name)
}
