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

package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
)

// RawWorker is a worker that a test drives by hand over a raw GetWorkItems
// stream, for tests that control every response the worker sends.
type RawWorker struct {
	client protos.TaskHubSidecarServiceClient
	items  chan *protos.WorkItem
}

// OpenRawWorker opens a GetWorkItems stream on conn. The stream ends with
// ctx.
func OpenRawWorker(ctx context.Context, conn grpc.ClientConnInterface) (*RawWorker, error) {
	cl := protos.NewTaskHubSidecarServiceClient(conn)
	if _, err := cl.Hello(ctx, new(emptypb.Empty)); err != nil {
		return nil, err
	}
	stream, err := cl.GetWorkItems(ctx, new(protos.GetWorkItemsRequest))
	if err != nil {
		return nil, err
	}

	w := &RawWorker{client: cl, items: make(chan *protos.WorkItem, 8)}
	go func() {
		defer close(w.items)
		for {
			wi, err := stream.Recv()
			if err != nil {
				return
			}
			if wi.GetHealthPing() != nil {
				continue
			}
			select {
			case w.items <- wi:
			case <-ctx.Done():
				return
			}
		}
	}()
	return w, nil
}

// ConnectRawWorker opens a RawWorker on daprd index 0.
func (w *Workflow) ConnectRawWorker(t *testing.T, ctx context.Context) *RawWorker {
	t.Helper()
	return w.ConnectRawWorkerN(t, ctx, 0)
}

// ConnectRawWorkerN opens a RawWorker on the daprd at the given index.
func (w *Workflow) ConnectRawWorkerN(t *testing.T, ctx context.Context, index int) *RawWorker {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")
	rw, err := OpenRawWorker(ctx, w.daprds[index].GRPCConn(t, ctx))
	require.NoError(t, err)
	return rw
}

// Client is the task hub client the worker answers its work items with.
func (r *RawWorker) Client() protos.TaskHubSidecarServiceClient {
	return r.client
}

// Items returns the work items the stream receives, health pings skipped.
// It is closed when the stream ends.
func (r *RawWorker) Items() <-chan *protos.WorkItem {
	return r.items
}

// NextWorkflowItem returns the next work item, which must be a workflow
// work item.
func (r *RawWorker) NextWorkflowItem(t *testing.T) *protos.WorkItem {
	t.Helper()
	select {
	case wi, ok := <-r.items:
		require.True(t, ok, "the work item stream ended")
		require.NotNil(t, wi.GetWorkflowRequest(), "expected a workflow work item, got %v", wi)
		return wi
	case <-time.After(30 * time.Second):
		require.FailNow(t, "timed out waiting for a workflow work item")
		return nil
	}
}

// CompletedResponse is the response that completes the workflow of wi.
func CompletedResponse(wi *protos.WorkItem) *protos.WorkflowResponse {
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

// StartWorkflow schedules a workflow with instance ID name without waiting
// for the answer, which daprd sends once the first turn is saved, so a test
// can drive that turn with a RawWorker. The answer is checked at cleanup.
func StartWorkflow(t *testing.T, ctx context.Context, cl *client.TaskHubGrpcClient, name string) api.InstanceID {
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
