/* Copyright 2023 The Dapr Authors
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

package workflow

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(noapp))
}

type noapp struct {
	daprd *daprd.Daprd
}

func (n *noapp) Setup(t *testing.T) []framework.Option {
	placement := placement.New(t)
	scheduler := scheduler.New(t)

	n.daprd = daprd.New(t,
		daprd.WithPlacementAddresses(placement.Address()),
		daprd.WithInMemoryActorStateStore("foo"),
		daprd.WithScheduler(scheduler),
	)

	return []framework.Option{
		framework.WithProcesses(placement, scheduler, n.daprd),
	}
}

func (n *noapp) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()

	var called atomic.Bool
	require.NoError(t, registry.AddOrchestratorN("noapp", func(ctx *task.OrchestrationContext) (any, error) {
		called.Store(true)
		return nil, nil
	}))
	client := client.NewTaskHubGrpcClient(n.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, client.StartWorkItemListener(ctx, registry))

	id, err := client.ScheduleNewOrchestration(ctx, "noapp")
	require.NoError(t, err)
	_, err = client.WaitForOrchestrationCompletion(ctx, id)
	require.NoError(t, err)

	assert.True(t, called.Load())
}
