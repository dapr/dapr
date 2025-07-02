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
	"fmt"
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
	suite.Register(new(restartmanyapps))
}

type restartmanyapps struct {
	place     *placement.Placement
	scheduler *scheduler.Scheduler
}

func (r *restartmanyapps) Setup(t *testing.T) []framework.Option {
	r.place = placement.New(t)
	r.scheduler = scheduler.New(t)

	return []framework.Option{
		framework.WithProcesses(r.place, r.scheduler),
	}
}

func (r *restartmanyapps) Run(t *testing.T, ctx context.Context) {
	r.scheduler.WaitUntilRunning(t, ctx)
	r.place.WaitUntilRunning(t, ctx)

	registry1 := task.NewTaskRegistry()
	registry2 := task.NewTaskRegistry()
	registry3 := task.NewTaskRegistry()

	// App2: First activity
	registry2.AddActivityN("process", func(c task.ActivityContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		return fmt.Sprintf("processed by app2: %s", input), nil
	})

	// App3: Second activity
	registry3.AddActivityN("transform", func(c task.ActivityContext) (any, error) {
		var input string
		if err := c.GetInput(&input); err != nil {
			return nil, err
		}
		return fmt.Sprintf("transformed by app3: %s", input), nil
	})

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()
	appID3 := uuid.New().String()

	// App1: Orchestrator that calls both app2 & app3
	registry1.AddOrchestratorN("multiapp", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		// Call app2
		var result1 string
		err := ctx.CallActivity("process",
			task.WithActivityInput(input),
			task.WithAppID(appID2)).Await(&result1)
		if err != nil {
			return nil, err
		}

		// Call app3
		var result2 string
		err = ctx.CallActivity("transform",
			task.WithActivityInput(result1),
			task.WithAppID(appID3)).Await(&result2)
		if err != nil {
			return nil, err
		}

		return result2, nil
	})

	timeTaken := make([]time.Duration, 0, 3)
	for range 3 {
		// Create app1 (orchestrator)
		daprd1 := daprd.New(t,
			daprd.WithPlacementAddresses(r.place.Address()),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithSchedulerAddresses(r.scheduler.Address()),
			daprd.WithAppID(appID1),
		)

		// Create app2 (first activity host)
		daprd2 := daprd.New(t,
			daprd.WithPlacementAddresses(r.place.Address()),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithSchedulerAddresses(r.scheduler.Address()),
			daprd.WithAppID(appID2),
		)

		// Create app3 (second activity host)
		daprd3 := daprd.New(t,
			daprd.WithPlacementAddresses(r.place.Address()),
			daprd.WithInMemoryActorStateStore("mystore"),
			daprd.WithSchedulerAddresses(r.scheduler.Address()),
			daprd.WithAppID(appID3),
		)

		daprd1.Run(t, ctx)
		daprd2.Run(t, ctx)
		daprd3.Run(t, ctx)
		daprd1.WaitUntilRunning(t, ctx)
		daprd2.WaitUntilRunning(t, ctx)
		daprd3.WaitUntilRunning(t, ctx)

		t.Cleanup(func() {
			daprd1.Cleanup(t)
			daprd2.Cleanup(t)
			daprd3.Cleanup(t)
		})

		wctx, cancel := context.WithCancel(ctx)
		client1 := client.NewTaskHubGrpcClient(daprd1.GRPCConn(t, wctx), backend.DefaultLogger())
		client2 := client.NewTaskHubGrpcClient(daprd2.GRPCConn(t, wctx), backend.DefaultLogger())
		client3 := client.NewTaskHubGrpcClient(daprd3.GRPCConn(t, wctx), backend.DefaultLogger())

		require.NoError(t, client1.StartWorkItemListener(wctx, registry1))
		require.NoError(t, client2.StartWorkItemListener(wctx, registry2))
		require.NoError(t, client3.StartWorkItemListener(wctx, registry3))

		now := time.Now()
		id, err := client1.ScheduleNewOrchestration(wctx, "multiapp",
			api.WithInstanceID("multiapp-restart"),
			api.WithInput("hello"))
		require.NoError(t, err)
		metadata, err := client1.WaitForOrchestrationCompletion(wctx, id, api.WithFetchPayloads(true))
		require.NoError(t, err)
		timeTaken = append(timeTaken, time.Since(now))

		expected := `"transformed by app3: processed by app2: hello"`
		assert.Equal(t, expected, metadata.GetOutput().GetValue())
		cancel()
		daprd1.Cleanup(t)
		daprd2.Cleanup(t)
		daprd3.Cleanup(t)
	}

	// Ensure all workflows take similar amounts of time.
	for _, d1 := range timeTaken {
		for _, d2 := range timeTaken {
			assert.InDelta(t, d1.Seconds(), d2.Seconds(), float64(time.Second))
		}
	}
}
