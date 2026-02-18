/*
Copyright 2023 The Dapr Authors
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
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(restart))
}

type restart struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (r *restart) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	r.place = placement.New(t)
	r.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(r.place, r.scheduler),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	registry.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		return nil, ctx.CallActivity("bar").Await(nil)
	})
	registry.AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		return "", nil
	})

	appID := uuid.New().String()

	timeTaken := make([]time.Duration, 0, 3)
	for range 3 {
		daprd := daprd.New(t,
			daprd.WithPlacementAddresses(r.place.Address()),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithSchedulerAddresses(r.scheduler.Address()),
			daprd.WithAppID(appID),
		)

		daprd.Run(t, ctx)
		daprd.WaitUntilRunning(t, ctx)
		t.Cleanup(func() {
			daprd.Cleanup(t)
		})

		wctx, cancel := context.WithCancel(ctx)
		client := client.NewTaskHubGrpcClient(daprd.GRPCConn(t, wctx), logger.New(t))
		require.NoError(t, client.StartWorkItemListener(wctx, registry))

		now := time.Now()
		id, err := client.ScheduleNewOrchestration(wctx, "foo", api.WithInstanceID("pauser"))
		require.NoError(t, err)
		_, err = client.WaitForOrchestrationCompletion(wctx, id)
		require.NoError(t, err)
		timeTaken = append(timeTaken, time.Since(now))
		cancel()
		daprd.Cleanup(t)
	}

	// Ensure all workflows take similar amounts of time.
	for _, d1 := range timeTaken {
		for _, d2 := range timeTaken {
			assert.InDelta(t, d1.Seconds(), d2.Seconds(), float64(time.Second))
		}
	}
}
