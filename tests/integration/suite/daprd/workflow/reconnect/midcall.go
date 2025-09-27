/*
Copyright 2025 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://wwb.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reconnect

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(midcall))
}

type midcall struct {
	workflow *workflow.Workflow
}

func (m *midcall) Setup(t *testing.T) []framework.Option {
	m.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(m.workflow),
	}
}

func (m *midcall) Run(t *testing.T, ctx context.Context) {
	m.workflow.WaitUntilRunning(t, ctx)

	var here atomic.Int64
	m.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		here.Add(1)
		return nil, ctx.WaitForSingleEvent("bar", time.Minute).Await(nil)
	})

	client1 := client.NewTaskHubGrpcClient(m.workflow.DaprN(0).GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, m.workflow.Registry()))

	client2 := client.NewTaskHubGrpcClient(m.workflow.DaprN(1).GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client2.StartWorkItemListener(ctx, m.workflow.Registry()))

	ids := make([]api.InstanceID, 5)
	var err error
	for i := range 5 {
		ids[i], err = client1.ScheduleNewOrchestration(ctx, "foo")
		require.NoError(t, err)
	}

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(5), here.Load())
	}, time.Second*10, time.Millisecond*10)

	errCh := make(chan error, 10)
	go func() {
		time.Sleep(time.Second * 5)
		m.workflow.DaprN(1).Kill(t)
		for _, id := range ids {
			errCh <- client1.RaiseEvent(ctx, id, "bar")
		}
	}()

	for _, id := range ids {
		go func(id api.InstanceID) {
			_, ferr := client1.WaitForOrchestrationStart(ctx, id)
			errCh <- ferr
		}(id)
	}

	for range 10 {
		select {
		case <-time.After(time.Second * 10):
			require.Fail(t, "timed out waiting for event to be raised")
		case err = <-errCh:
			require.NoError(t, err)
		}
	}
}
