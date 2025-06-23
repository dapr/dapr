/*
Copyright 2025 The Dapr Authors
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

package crossapp

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
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
	r.place = placement.New(t)
	r.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(r.place, r.scheduler),
	}
}

func (r *restart) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)

	registry1 := task.NewTaskRegistry()
	registry2 := task.NewTaskRegistry()

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()

	// App2: Activity that will be called by app1
	registry2.AddActivityN("bar", func(c task.ActivityContext) (any, error) {
		return "processed by app2", nil
	})

	// App1: Orchestrator that calls app2
	registry1.AddOrchestratorN("foo", func(ctx *task.OrchestrationContext) (any, error) {
		var result string
		err := ctx.CallActivity("bar",
			task.WithAppID(appID2),
		).Await(&result)
		if err != nil {
			return nil, err
		}
		return result, nil
	})

	timeTaken := make([]time.Duration, 0, 5)
	for range 5 {
		// Create app1 (orchestrator)
		daprd1 := daprd.New(t,
			daprd.WithPlacementAddresses(r.place.Address()),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithSchedulerAddresses(r.scheduler.Address()),
			daprd.WithAppID(appID1),
		)

		// Create app2 (activity host)
		daprd2 := daprd.New(t,
			daprd.WithPlacementAddresses(r.place.Address()),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithSchedulerAddresses(r.scheduler.Address()),
			daprd.WithAppID(appID2),
		)

		daprd1.Run(t, ctx)
		daprd2.Run(t, ctx)
		daprd1.WaitUntilRunning(t, ctx)
		daprd2.WaitUntilRunning(t, ctx)
		t.Cleanup(func() {
			daprd1.Cleanup(t)
			daprd2.Cleanup(t)
		})

		wctx, cancel := context.WithCancel(ctx)
		client1 := client.NewTaskHubGrpcClient(daprd1.GRPCConn(t, wctx), backend.DefaultLogger())
		client2 := client.NewTaskHubGrpcClient(daprd2.GRPCConn(t, wctx), backend.DefaultLogger())

		require.NoError(t, client1.StartWorkItemListener(wctx, registry1))
		require.NoError(t, client2.StartWorkItemListener(wctx, registry2))

		now := time.Now()
		id, err := client1.ScheduleNewOrchestration(wctx, "foo", api.WithInstanceID("crossapp-restart"))
		require.NoError(t, err)
		_, err = client1.WaitForOrchestrationCompletion(wctx, id)
		require.NoError(t, err)
		timeTaken = append(timeTaken, time.Since(now))
		cancel()
		daprd1.Cleanup(t)
		daprd2.Cleanup(t)
	}

	// Ensure all workflows take similar amounts of time.
	for _, d1 := range timeTaken {
		for _, d2 := range timeTaken {
			assert.InDelta(t, d1.Seconds(), d2.Seconds(), float64(time.Second))
		}
	}
}
