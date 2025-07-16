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

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(activity))
}

type activity struct {
	workflow *workflow.Workflow
}

func (a *activity) Setup(t *testing.T) []framework.Option {
	a.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithWorkflowsEnableClusteredDeployment(true)),
		workflow.WithDaprdOptions(1, daprd.WithWorkflowsEnableClusteredDeployment(true)),
	)

	return []framework.Option{
		framework.WithProcesses(a.workflow),
	}
}

func (a *activity) Run(t *testing.T, ctx context.Context) {
	a.workflow.WaitUntilRunning(t, ctx)

	a.workflow.Registry().AddOrchestratorN("activity", func(ctx *task.OrchestrationContext) (any, error) {
		require.NoError(t, ctx.CallActivity("abc", task.WithActivityInput("abc")).Await(nil))
		return nil, nil
	})
	a.workflow.Registry().AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
		return nil, nil
	})

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		a.workflow.DaprN(0).GRPCConn(t, ctx),
		a.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	require.NoError(t, client.StartWorkItemListener(ctx, a.workflow.Registry()))

	const n = 10
	ids := make([]api.InstanceID, n)

	var err error
	for i := range n {
		ids[i], err = client.ScheduleNewOrchestration(ctx, "activity")
		require.NoError(t, err)
	}

	for i := range n {
		_, err = client.WaitForOrchestrationCompletion(ctx, ids[i])
		require.NoError(t, err)
	}
}
