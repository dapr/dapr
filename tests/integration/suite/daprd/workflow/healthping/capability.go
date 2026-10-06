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

package healthping

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api/protos"
)

const pingInterval = time.Millisecond * 100

func init() {
	suite.Register(new(capability))
}

// capability verifies over the raw task hub protocol that daprd sends
// HealthPings on an idle work item stream only when the worker advertised
// WORKER_CAPABILITY_HEALTH_PING. Older workers mishandle work item types they
// do not know, so they must never receive one.
type capability struct {
	workflow *workflow.Workflow
}

func (c *capability) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithWorkflowHealthPingInterval(t, pingInterval)),
	)
	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *capability) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(c.workflow.Dapr().GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	sc := protos.NewTaskHubSidecarServiceClient(conn)
	_, err = sc.Hello(ctx, new(emptypb.Empty))
	require.NoError(t, err)

	recv := func(caps ...protos.WorkerCapability) <-chan *protos.WorkItem {
		stream, err := sc.GetWorkItems(ctx, &protos.GetWorkItemsRequest{Capabilities: caps})
		require.NoError(t, err)
		ch := make(chan *protos.WorkItem, 8)
		go func() {
			defer close(ch)
			for {
				wi, err := stream.Recv()
				if err != nil {
					return
				}
				ch <- wi
			}
		}()
		return ch
	}

	pinged := recv(protos.WorkerCapability_WORKER_CAPABILITY_HEALTH_PING)
	unpinged := recv(protos.WorkerCapability_WORKER_CAPABILITY_STATEFUL_HISTORY)

	select {
	case wi, ok := <-pinged:
		require.True(t, ok, "stream advertising the capability closed before a health ping arrived")
		require.NotNil(t, wi.GetHealthPing(), "expected a health ping, got %v", wi)
	case <-time.After(time.Second * 5):
		require.Fail(t, "no health ping on the stream that advertised the capability")
	}

	// Ten ping intervals: an ungated ping would have arrived by now.
	select {
	case wi, ok := <-unpinged:
		require.Fail(t, "stream without the capability received a work item or closed", "item=%v open=%v", wi, ok)
	case <-time.After(pingInterval * 10):
	}
}
