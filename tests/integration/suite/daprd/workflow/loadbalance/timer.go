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
	suite.Register(new(timer))
}

type timer struct {
	workflow *workflow.Workflow
}

func (i *timer) Setup(t *testing.T) []framework.Option {
	i.workflow = newClusteredDeployment(t, 2)

	return []framework.Option{
		framework.WithProcesses(i.workflow),
	}
}

func (i *timer) Run(t *testing.T, ctx context.Context) {
	i.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, i.workflow.RegistryN(0).AddOrchestratorN("timer", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CreateTimer(time.Second).Await(nil))
		return nil, nil
	}))
	_ = i.workflow.BackendClientN(t, ctx, 0)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		i.workflow.DaprN(0).GRPCConn(t, ctx),
		i.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	const n = 10
	ids := make([]api.InstanceID, n)

	var err error
	for i := range n {
		ids[i], err = client.ScheduleNewOrchestration(ctx, "timer")
		require.NoError(t, err)
	}

	for i := range n {
		_, err = client.WaitForOrchestrationCompletion(ctx, ids[i])
		require.NoError(t, err)
	}
}
