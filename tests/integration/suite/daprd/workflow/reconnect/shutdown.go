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

package reconnect

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(shutdownWait))
}

// shutdownWait verifies that when daprd shuts down while a client is waiting
// for workflow completion, the wait RPC returns codes.Unavailable instead of
// codes.Canceled, allowing the client to retry and succeed against the restarted
// sidecar. It also verifies that client-side cancellation continues to return
// codes.Canceled.
type shutdownWait struct {
	workflow *workflow.Workflow
}

func (s *shutdownWait) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *shutdownWait) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	waitCh := make(chan struct{})
	s.workflow.Registry().AddWorkflowN("longrunning", func(ctx *task.WorkflowContext) (any, error) {
		<-waitCh
		return "completed-successfully", nil
	})

	c := client.NewTaskHubGrpcClient(s.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, c.StartWorkItemListener(ctx, s.workflow.Registry()))

	id, err := c.ScheduleNewWorkflow(ctx, "longrunning", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	sidecar := protos.NewTaskHubSidecarServiceClient(s.workflow.Dapr().GRPCConn(t, ctx))

	// In-flight wait blocked on the gRPC endpoint while daprd shuts down
	waitErrCh := make(chan error, 1)
	go func() {
		_, err := sidecar.WaitForInstanceCompletion(ctx, &protos.GetInstanceRequest{InstanceId: string(id)})
		waitErrCh <- err
	}()

	// Allow the stream to establish with the actor router
	time.Sleep(100 * time.Millisecond)

	// Graceful restart of daprd initiates runtime shutdown
	s.workflow.Dapr().RestartGraceful(t, ctx)

	// Verify the in-flight wait returned codes.Unavailable instead of codes.Canceled
	select {
	case err := <-waitErrCh:
		require.Error(t, err)
		assert.Equal(t, codes.Unavailable, status.Code(err), "expected codes.Unavailable on runtime shutdown, got %v", err)
	case <-time.After(time.Second * 15):
		t.Fatal("timed out waiting for in-flight WaitForInstanceCompletion to unblock on daprd shutdown")
	}

	// Reconnect worker against the restarted sidecar
	s.workflow.Dapr().WaitUntilRunning(t, ctx)
	cRestarted := client.NewTaskHubGrpcClient(s.workflow.Dapr().GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, cRestarted.StartWorkItemListener(ctx, s.workflow.Registry()))

	// Unblock workflow
	close(waitCh)

	// Re-issued wait against the restarted sidecar completes normally
	meta, err := cRestarted.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, meta.GetRuntimeStatus())

	// Verify that caller-initiated context cancellation still yields codes.Canceled
	sidecarRestarted := protos.NewTaskHubSidecarServiceClient(s.workflow.Dapr().GRPCConn(t, ctx))
	callerCtx, cancelCaller := context.WithCancel(ctx)
	cancelCaller()
	_, err = sidecarRestarted.WaitForInstanceCompletion(callerCtx, &protos.GetInstanceRequest{InstanceId: string(id)})
	require.Error(t, err)
	assert.Equal(t, codes.Canceled, status.Code(err), "expected codes.Canceled on caller cancellation, got %v", err)
}
