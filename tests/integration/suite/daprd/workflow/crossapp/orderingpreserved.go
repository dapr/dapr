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
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/backend"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(orderingpreserved))
}

// orderingpreserved demonstrates that activity execution order is preserved
// across local and cross-app calls using an atomic slice to track order
type orderingpreserved struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	place  *placement.Placement
	sched  *scheduler.Scheduler

	registry1 *task.TaskRegistry
	registry2 *task.TaskRegistry

	executionOrder atomic.Value
	activityCount  atomic.Int32
}

func (o *orderingpreserved) Setup(t *testing.T) []framework.Option {
	o.place = placement.New(t,
		placement.WithLogLevel("debug"))
	o.sched = scheduler.New(t,
		scheduler.WithLogLevel("debug"))
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithMetadata("busyTimeout", "10s"),
		sqlite.WithMetadata("disableWAL", "true"),
	)

	app1 := app.New(t)
	app2 := app.New(t)

	o.executionOrder.Store([]string{})

	// Create registries for each app
	o.registry1 = task.NewTaskRegistry()
	o.registry2 = task.NewTaskRegistry()

	// Helper function to record execution order atomically
	recordExecution := func(activityName, appID string) {
		o.activityCount.Add(1)
		order := o.executionOrder.Load()

		orderSlice := order.([]string)
		orderSlice = append(orderSlice, activityName)
		o.executionOrder.Store(orderSlice)
	}

	appID1 := uuid.New().String()
	appID2 := uuid.New().String()

	// App1: Local activities
	o.registry1.AddActivityN("LocalActivity1", func(ctx task.ActivityContext) (any, error) {
		recordExecution("LocalActivity1", appID1)
		time.Sleep(10 * time.Millisecond) // Small delay to ensure ordering is tested
		return "local1", nil
	})

	o.registry1.AddActivityN("LocalActivity3", func(ctx task.ActivityContext) (any, error) {
		recordExecution("LocalActivity3", appID1)
		time.Sleep(10 * time.Millisecond)
		return "local3", nil
	})

	o.registry1.AddActivityN("LocalActivity5", func(ctx task.ActivityContext) (any, error) {
		recordExecution("LocalActivity5", appID1)
		time.Sleep(10 * time.Millisecond)
		return "local5", nil
	})

	o.registry1.AddActivityN("LocalActivity7", func(ctx task.ActivityContext) (any, error) {
		recordExecution("LocalActivity7", appID1)
		time.Sleep(10 * time.Millisecond)
		return "local7", nil
	})

	// App2: Remote activities
	o.registry2.AddActivityN("RemoteActivity2", func(ctx task.ActivityContext) (any, error) {
		recordExecution("RemoteActivity2", appID2)
		time.Sleep(10 * time.Millisecond)
		return "remote2", nil
	})

	o.registry2.AddActivityN("RemoteActivity4", func(ctx task.ActivityContext) (any, error) {
		recordExecution("RemoteActivity4", appID2)
		time.Sleep(10 * time.Millisecond)
		return "remote4", nil
	})

	o.registry2.AddActivityN("RemoteActivity6", func(ctx task.ActivityContext) (any, error) {
		recordExecution("RemoteActivity6", appID2)
		time.Sleep(10 * time.Millisecond)
		return "remote6", nil
	})

	// App1: Orchestrator - calls activities in specific order
	o.registry1.AddOrchestratorN("OrderingWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, fmt.Errorf("failed to get input in orchestrator: %w", err)
		}

		// Call activities in specific order: local1 -> remote2 -> local3 -> remote4 -> local5 -> remote6 -> local7
		var results []string

		// Step 1: Local activity
		var result1 string
		err := ctx.CallActivity("LocalActivity1",
			task.WithActivityInput(input)).
			Await(&result1)
		if err != nil {
			return nil, fmt.Errorf("failed to execute LocalActivity1: %w", err)
		}
		results = append(results, result1)

		// Step 2: Remote activity
		var result2 string
		err = ctx.CallActivity("RemoteActivity2",
			task.WithActivityInput(result1),
			task.WithAppID(appID2)).
			Await(&result2)
		if err != nil {
			return nil, fmt.Errorf("failed to execute RemoteActivity2: %w", err)
		}
		results = append(results, result2)

		// Step 3: Local activity
		var result3 string
		err = ctx.CallActivity("LocalActivity3",
			task.WithActivityInput(result2)).
			Await(&result3)
		if err != nil {
			return nil, fmt.Errorf("failed to execute LocalActivity3: %w", err)
		}
		results = append(results, result3)

		// Step 4: Remote activity
		var result4 string
		err = ctx.CallActivity("RemoteActivity4",
			task.WithActivityInput(result3),
			task.WithAppID(appID2)).
			Await(&result4)
		if err != nil {
			return nil, fmt.Errorf("failed to execute RemoteActivity4: %w", err)
		}
		results = append(results, result4)

		// Step 5: Local activity
		var result5 string
		err = ctx.CallActivity("LocalActivity5",
			task.WithActivityInput(result4)).
			Await(&result5)
		if err != nil {
			return nil, fmt.Errorf("failed to execute LocalActivity5: %w", err)
		}
		results = append(results, result5)

		// Step 6: Remote activity
		var result6 string
		err = ctx.CallActivity("RemoteActivity6",
			task.WithActivityInput(result5),
			task.WithAppID(appID2)).
			Await(&result6)
		if err != nil {
			return nil, fmt.Errorf("failed to execute RemoteActivity6: %w", err)
		}
		results = append(results, result6)

		// Step 7: Local activity
		var result7 string
		err = ctx.CallActivity("LocalActivity7",
			task.WithActivityInput(result6)).
			Await(&result7)
		if err != nil {
			return nil, fmt.Errorf("failed to execute LocalActivity7: %w", err)
		}
		results = append(results, result7)

		return results, nil
	})

	o.daprd1 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(o.place.Address()),
		daprd.WithScheduler(o.sched),
		daprd.WithAppID(appID1),
		daprd.WithAppPort(app1.Port()),
		daprd.WithLogLevel("debug"),
	)
	o.daprd2 = daprd.New(t,
		daprd.WithInMemoryActorStateStore("mystore"),
		daprd.WithPlacementAddresses(o.place.Address()),
		daprd.WithScheduler(o.sched),
		daprd.WithAppID(appID2),
		daprd.WithAppPort(app2.Port()),
		daprd.WithLogLevel("debug"),
	)

	return []framework.Option{
		framework.WithProcesses(o.place, o.sched, db, app1, app2, o.daprd1, o.daprd2),
	}
}

func (o *orderingpreserved) Run(t *testing.T, ctx context.Context) {
	o.sched.WaitUntilRunning(t, ctx)
	o.place.WaitUntilRunning(t, ctx)
	o.daprd1.WaitUntilRunning(t, ctx)
	o.daprd2.WaitUntilRunning(t, ctx)

	// Start workflow listeners for each app
	client1 := client.NewTaskHubGrpcClient(o.daprd1.GRPCConn(t, ctx), backend.DefaultLogger())
	client2 := client.NewTaskHubGrpcClient(o.daprd2.GRPCConn(t, ctx), backend.DefaultLogger())

	// Start listeners for each app
	err := client1.StartWorkItemListener(ctx, o.registry1)
	require.NoError(t, err)
	err = client2.StartWorkItemListener(ctx, o.registry2)
	require.NoError(t, err)

	// Start the ordering workflow
	id, err := client1.ScheduleNewOrchestration(ctx, "OrderingWorkflow", api.WithInput("start"))
	require.NoError(t, err)

	metadata, err := client1.WaitForOrchestrationCompletion(ctx, id, api.WithFetchPayloads(true))
	assert.NoError(t, err)

	assert.True(t, api.OrchestrationMetadataIsComplete(metadata))
	assert.Equal(t, api.RUNTIME_STATUS_COMPLETED, metadata.RuntimeStatus)

	// Verify all activities were called & ordering
	expectedActivityCount := int32(7)
	actualActivityCount := o.activityCount.Load()
	assert.Equal(t, expectedActivityCount, actualActivityCount, "Expected %d activities to be called, got %d", expectedActivityCount, actualActivityCount)

	actualOrder := o.executionOrder.Load()
	actualOrderSlice := actualOrder.([]string)
	expectedOrder := []string{
		"LocalActivity1",
		"RemoteActivity2",
		"LocalActivity3",
		"RemoteActivity4",
		"LocalActivity5",
		"RemoteActivity6",
		"LocalActivity7",
	}

	assert.Equal(t, len(expectedOrder), len(actualOrderSlice), "Expected %d activities to execute, got %d", len(expectedOrder), len(actualOrderSlice))
	for i, expected := range expectedOrder {
		if i < len(actualOrderSlice) {
			assert.Equal(t, expected, actualOrderSlice[i], "Activity %d execution order mismatch", i+1)
		}
	}
	expectedResult := `["local1","remote2","local3","remote4","local5","remote6","local7"]`
	assert.Equal(t, expectedResult, metadata.GetOutput().GetValue())
}
