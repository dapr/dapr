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
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(waitfor))
}

type waitfor struct {
	workflow *workflow.Workflow
}

func (w *waitfor) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.New(t,
		workflow.WithDaprds(2),
	)

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *waitfor) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	var here atomic.Bool
	w.workflow.Registry().AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		here.Store(true)
		return nil, ctx.WaitForSingleEvent("bar", time.Minute).Await(nil)
	})

	client1 := client.NewTaskHubGrpcClient(w.workflow.DaprN(0).GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client1.StartWorkItemListener(ctx, w.workflow.Registry()))

	ctxA, cancelA := context.WithCancel(ctx)
	client2 := client.NewTaskHubGrpcClient(w.workflow.DaprN(1).GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client2.StartWorkItemListener(ctxA, w.workflow.Registry()))

	id, err := client1.ScheduleNewOrchestration(ctx, "foo")
	require.NoError(t, err)

	assert.Eventually(t, here.Load, time.Second*10, time.Millisecond*10)

	errCh := make(chan error, 1)
	go func() {
		time.Sleep(time.Second * 2)
		cancelA()
		errCh <- client1.RaiseEvent(ctx, id, "bar")
	}()

	_, err = client1.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	select {
	case <-time.After(time.Second * 10):
		require.Fail(t, "timed out waiting for event to be raised")
	case err = <-errCh:
		require.NoError(t, err)
	}
}
