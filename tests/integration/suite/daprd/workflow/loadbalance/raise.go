/*
Copyright 2024 The Dapr Authors
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
	suite.Register(new(raise))
}

type raise struct {
	workflow *workflow.Workflow
}

func (r *raise) Setup(t *testing.T) []framework.Option {
	r.workflow = newClusteredDeployment(t, 2)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *raise) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, r.workflow.RegistryN(0).AddOrchestratorN("raise", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.WaitForSingleEvent("my-event", time.Hour).Await(nil))
		return nil, nil
	}))
	_ = r.workflow.BackendClientN(t, ctx, 0)
	// verify executor actor is registered
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.GreaterOrEqual(col,
			len(r.workflow.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		r.workflow.DaprN(0).GRPCConn(t, ctx),
		r.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	const n = 10
	ids := make([]api.InstanceID, n)

	var err error
	for i := range n {
		ids[i], err = client.ScheduleNewOrchestration(ctx, "raise")
		require.NoError(t, err)
	}

	for i := range n {
		require.NoError(t, client.RaiseEvent(ctx, ids[i], "my-event"))
	}

	for i := range n {
		_, err = client.WaitForOrchestrationCompletion(ctx, ids[i])
		require.NoError(t, err)
	}
}
