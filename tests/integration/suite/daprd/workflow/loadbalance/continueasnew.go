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

package loadbalance

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(continueasnew))
}

type continueasnew struct {
	workflow *workflow.Workflow
}

func (c *continueasnew) Setup(t *testing.T) []framework.Option {
	c.workflow = newClusteredDeployment(t, 2)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *continueasnew) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	var cont atomic.Bool
	require.NoError(t, c.workflow.RegistryN(0).AddOrchestratorN("can", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		require.NoError(t, ctx.GetInput(&input))
		if cont.Load() {
			assert.Equal(t, "second call", input)
		} else {
			assert.Equal(t, "first call", input)
		}

		if cont.CompareAndSwap(false, true) {
			ctx.ContinueAsNew("second call")
		}

		return nil, nil
	}))
	_ = c.workflow.BackendClientN(t, ctx, 0)
	// verify executor actor is registered
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(c.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		c.workflow.DaprN(0).GRPCConn(t, ctx),
		c.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	for range 10 {
		cont.Store(false)
		id, err := client.ScheduleNewOrchestration(ctx, "can", api.WithInput("first call"))
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(ctx, id)
		require.NoError(t, err)
	}
}
