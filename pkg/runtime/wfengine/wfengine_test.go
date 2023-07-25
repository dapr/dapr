/*
Copyright 2023 The Dapr Authors
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

// wfengine_test is a suite of integration tests that verify workflow
// engine behavior using only exported APIs.
package wfengine_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const testAppID = "wf-app"

func fakeStore() state.Store {
	return daprt.NewFakeStateStore()
}

type mockPlacement struct{}

func (p *mockPlacement) AddHostedActorType(actorType string) error {
	return nil
}

func NewMockPlacement() actors.PlacementService {
	return &mockPlacement{}
}

// LookupActor implements internal.PlacementService
func (*mockPlacement) LookupActor(actorType string, actorID string) (name string, appID string) {
	return "localhost", testAppID
}

// Start implements internal.PlacementService
func (*mockPlacement) Start(ctx context.Context) error {
	return nil
}

// Stop implements internal.PlacementService
func (*mockPlacement) Close() error {
	return nil
}

// WaitUntilReady implements internal.PlacementService
func (*mockPlacement) WaitUntilReady(ctx context.Context) error {
	return nil
}

// TestStartWorkflowEngine validates that starting the workflow engine returns no errors.
func TestStartWorkflowEngine(t *testing.T) {
	ctx := context.Background()
	engine := getEngine(t)
	engine.ConfigureGrpcExecutor()
	grpcServer := grpc.NewServer()
	engine.RegisterGrpcServer(grpcServer)
	err := engine.Start(ctx)
	assert.NoError(t, err)
}

// GetTestOptions returns an array of functions for configuring the workflow engine. Each
// string returned by each function can be used as the name of the test configuration.
func GetTestOptions() []func(wfe *wfengine.WorkflowEngine) string {
	return []func(wfe *wfengine.WorkflowEngine) string{
		func(wfe *wfengine.WorkflowEngine) string {
			// caching enabled, etc.
			return "default options"
		},
		func(wfe *wfengine.WorkflowEngine) string {
			// disable caching to test recovery from failure
			wfe.DisableActorCaching(true)
			return "caching disabled"
		},
	}
}

// TestEmptyWorkflow executes a no-op workflow end-to-end and verifies all workflow metadata is correctly initialized.
func TestEmptyWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("EmptyWorkflow", func(*task.OrchestrationContext) (any, error) {
		return nil, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			preStartTime := time.Now().UTC()
			id, err := client.ScheduleNewOrchestration(ctx, "EmptyWorkflow")
			require.NoError(t, err)

			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)

			assert.Equal(t, id, metadata.InstanceID)
			assert.True(t, metadata.IsComplete())
			assert.GreaterOrEqual(t, metadata.CreatedAt, preStartTime)
			assert.GreaterOrEqual(t, metadata.LastUpdatedAt, metadata.CreatedAt)
			assert.Empty(t, metadata.SerializedInput)
			assert.Empty(t, metadata.SerializedOutput)
			assert.Empty(t, metadata.SerializedCustomStatus)
			assert.Nil(t, metadata.FailureDetails)
		})
	}
}

// TestSingleTimerWorkflow executes a workflow schedules a timer and completes, verifying that timers
// can be used to resume a workflow. This test does not attempt to verify delay accuracy.
func TestSingleTimerWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("SingleTimer", func(ctx *task.OrchestrationContext) (any, error) {
		err := ctx.CreateTimer(time.Duration(0)).Await(nil)
		return nil, err
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "SingleTimer")
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)

			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.GreaterOrEqual(t, metadata.LastUpdatedAt, metadata.CreatedAt)
		})
	}
}

// TestSingleActivityWorkflow executes a workflow that calls a single activity and completes. The input
// passed to the workflow is also passed to the activity, and the activity's return value is also returned
// by the workflow, allowing the test to verify input and output handling, as well as activity execution.
func TestSingleActivityWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("SingleActivity", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallActivity("SayHello", task.WithActivityInput(input)).Await(&output)
		return output, err
	})
	r.AddActivityN("SayHello", func(ctx task.ActivityContext) (any, error) {
		var name string
		if err := ctx.GetInput(&name); err != nil {
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", name), nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("世界"))
			require.NoError(t, err)

			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `"世界"`, metadata.SerializedInput)
			assert.Equal(t, `"Hello, 世界!"`, metadata.SerializedOutput)
		})
	}
}

// TestActivityChainingWorkflow verifies that a workflow can call multiple activities in a sequence,
// passing the output of the previous activity as the input of the next activity.
func TestActivityChainingWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("ActivityChain", func(ctx *task.OrchestrationContext) (any, error) {
		val := 0
		for i := 0; i < 10; i++ {
			if err := ctx.CallActivity("PlusOne", task.WithActivityInput(val)).Await(&val); err != nil {
				return nil, err
			}
		}
		return val, nil
	})
	r.AddActivityN("PlusOne", func(ctx task.ActivityContext) (any, error) {
		var input int
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return input + 1, nil
	})

	ctx := context.Background()
	client, engine, _ := startEngineAndGetStore(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ActivityChain")
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, `10`, metadata.SerializedOutput)
				}
			}
		})
	}
}

// TestConcurrentActivityExecution verifies that a workflow can execute multiple activities in parallel
// and wait for all of them to complete before completing itself.
func TestConcurrentActivityExecution(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("ActivityFanOut", func(ctx *task.OrchestrationContext) (any, error) {
		tasks := []task.Task{}
		for i := 0; i < 10; i++ {
			tasks = append(tasks, ctx.CallActivity("ToString", task.WithActivityInput(i)))
		}
		results := []string{}
		for _, t := range tasks {
			var result string
			if err := t.Await(&result); err != nil {
				return nil, err
			}
			results = append(results, result)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(results)))
		return results, nil
	})
	r.AddActivityN("ToString", func(ctx task.ActivityContext) (any, error) {
		var input int
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		// Sleep for 1 second to ensure that the test passes only if all activities execute in parallel.
		time.Sleep(1 * time.Second)
		return fmt.Sprintf("%d", input), nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ActivityFanOut")
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, `["9","8","7","6","5","4","3","2","1","0"]`, metadata.SerializedOutput)

					// Because all the activities run in parallel, they should complete very quickly
					assert.Less(t, metadata.LastUpdatedAt.Sub(metadata.CreatedAt), 3*time.Second)
				}
			}
		})
	}
}

// TestContinueAsNewWorkflow verifies that a workflow can "continue-as-new" to restart itself with a new input.
func TestContinueAsNewWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("ContinueAsNewTest", func(ctx *task.OrchestrationContext) (any, error) {
		var input int32
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input < 10 {
			if err := ctx.CreateTimer(0).Await(nil); err != nil {
				return nil, err
			}
			var nextInput int32
			if err := ctx.CallActivity("PlusOne", task.WithActivityInput(input)).Await(&nextInput); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(nextInput)
		}
		return input, nil
	})
	r.AddActivityN("PlusOne", func(ctx task.ActivityContext) (any, error) {
		var input int32
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return input + 1, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ContinueAsNewTest", api.WithInput(0))
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)

			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `10`, metadata.SerializedOutput)
		})
	}
}

// TestRecreateCompletedWorkflow verifies that completed workflows can be restarted with new inputs externally.
func TestRecreateCompletedWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("EchoWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var input any
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return input, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			// First workflow
			var metadata *api.OrchestrationMetadata
			id, err := client.ScheduleNewOrchestration(ctx, "EchoWorkflow", api.WithInput("echo!"))
			if assert.NoError(t, err) {
				if metadata, err = client.WaitForOrchestrationCompletion(ctx, id); assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, `"echo!"`, metadata.SerializedOutput)
				}
			}

			// Second workflow, using the same ID as the first but a different input
			_, err = client.ScheduleNewOrchestration(ctx, "EchoWorkflow", api.WithInstanceID(id), api.WithInput(42))
			if assert.NoError(t, err) {
				if metadata, err = client.WaitForOrchestrationCompletion(ctx, id); assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, `42`, metadata.SerializedOutput)
				}
			}
		})
	}
}

func TestInternalActorsSetupForWF(t *testing.T) {
	ctx := context.Background()
	_, engine := startEngine(ctx, t, task.NewTaskRegistry())
	assert.Equal(t, 2, len(engine.InternalActors()))
	assert.Contains(t, engine.InternalActors(), workflowActorType)
	assert.Contains(t, engine.InternalActors(), activityActorType)
}

// TestRecreateRunningWorkflowFails verifies that a workflow can't be recreated if it's in a running state.
func TestRecreateRunningWorkflowFails(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("SleepyWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		err := ctx.CreateTimer(24 * time.Hour).Await(nil)
		return nil, err
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			// Start the first workflow, which will not complete
			var metadata *api.OrchestrationMetadata
			id, err := client.ScheduleNewOrchestration(ctx, "SleepyWorkflow")
			if assert.NoError(t, err) {
				if metadata, err = client.WaitForOrchestrationStart(ctx, id); assert.NoError(t, err) {
					assert.False(t, metadata.IsComplete())
				}
			}

			// Attempting to start a second workflow with the same ID should fail
			_, err = client.ScheduleNewOrchestration(ctx, "SleepyWorkflow", api.WithInstanceID(id))
			require.Error(t, err)
			// We expect that the workflow instance ID is included in the error message
			assert.Contains(t, err.Error(), id)
		})
	}
}

// TestRetryWorkflowOnTimeout verifies that workflow operations are retried when they fail to complete.
func TestRetryWorkflowOnTimeout(t *testing.T) {
	const expectedCallCount = 3
	actualCallCount := 0

	r := task.NewTaskRegistry()
	r.AddOrchestratorN("FlakyWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		// update this global counter each time the workflow gets invoked
		actualCallCount++
		if actualCallCount < expectedCallCount {
			// simulate a hang for the first two calls
			time.Sleep(5 * time.Minute)
		}
		return actualCallCount, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)

	// Set a really short timeout to override the default workflow timeout so that we can exercise the timeout
	// handling codepath in a short period of time.
	engine.SetWorkflowTimeout(1 * time.Second)

	// Set a really short reminder interval to retry workflows immediately after they time out.
	engine.SetActorReminderInterval(1 * time.Millisecond)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			actualCallCount = 0

			id, err := client.ScheduleNewOrchestration(ctx, "FlakyWorkflow")
			require.NoError(t, err)
			// Add a 5 second timeout so that the test doesn't take forever if something isn't working
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, fmt.Sprintf("%d", expectedCallCount), metadata.SerializedOutput)
		})
	}
}

// TestRetryActivityOnTimeout verifies that activity operations are retried when they fail to complete.
func TestRetryActivityOnTimeout(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("FlakyWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var output int
		err := ctx.CallActivity("FlakyActivity").Await(&output)
		return output, err
	})

	const expectedCallCount = 3
	actualCallCount := 0

	r.AddActivityN("FlakyActivity", func(ctx task.ActivityContext) (any, error) {
		// update this global counter each time the activity gets invoked
		actualCallCount++
		if actualCallCount < expectedCallCount {
			// simulate a hang for the first two calls
			time.Sleep(5 * time.Minute)
		}
		return actualCallCount, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)

	// Set a really short timeout to override the default activity timeout (1 hour at the time of writing)
	// so that we can exercise the timeout handling codepath in a short period of time.
	engine.SetActivityTimeout(1 * time.Second)

	// Set a really short reminder interval to retry activities immediately after they time out.
	engine.SetActorReminderInterval(1 * time.Millisecond)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			actualCallCount = 0

			id, err := client.ScheduleNewOrchestration(ctx, "FlakyWorkflow")
			require.NoError(t, err)
			// Add a 5 second timeout so that the test doesn't take forever if something isn't working
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, fmt.Sprintf("%d", expectedCallCount), metadata.SerializedOutput)
		})
	}
}

func TestConcurrentTimerExecution(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("TimerFanOut", func(ctx *task.OrchestrationContext) (any, error) {
		tasks := []task.Task{}
		for i := 0; i < 2; i++ {
			tasks = append(tasks, ctx.CreateTimer(1*time.Second))
		}
		for _, t := range tasks {
			if err := t.Await(nil); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "TimerFanOut")
			if assert.NoError(t, err) {
				// Add a 5 second timeout so that the test doesn't take forever if something isn't working
				timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())

					// Because all the timers run in parallel, they should complete very quickly
					assert.Less(t, metadata.LastUpdatedAt.Sub(metadata.CreatedAt), 3*time.Second)
				}
			}
		})
	}
}

// TestRaiseEvent verifies that a workflow can have an event raised against it to trigger specific functionality.
func TestRaiseEvent(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("WorkflowForRaiseEvent", func(ctx *task.OrchestrationContext) (any, error) {
		var nameInput string
		if err := ctx.WaitForSingleEvent("NameOfEventBeingRaised", 30*time.Second).Await(&nameInput); err != nil {
			// Timeout expired
			return nil, err
		}
		return fmt.Sprintf("Hello, %s!", nameInput), nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "WorkflowForRaiseEvent")
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationStart(ctx, id)
				if assert.NoError(t, err) {
					assert.Equal(t, id, metadata.InstanceID)
					client.RaiseEvent(ctx, id, "NameOfEventBeingRaised", api.WithEventPayload("NameOfInput"))
					metadata, _ = client.WaitForOrchestrationCompletion(ctx, id)
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, `"Hello, NameOfInput!"`, metadata.SerializedOutput)
					assert.Nil(t, metadata.FailureDetails)
				}
			}
		})
	}
}

// TestContinueAsNew_WithEvents verifies that a workflow can continue as new and process any received events
// in subsequent iterations.
func TestContinueAsNew_WithEvents(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("ContinueAsNewTest", func(ctx *task.OrchestrationContext) (any, error) {
		var input int32
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var complete bool
		if err := ctx.WaitForSingleEvent("MyEvent", -1).Await(&complete); err != nil {
			return nil, err
		}
		if complete {
			return input, nil
		}
		ctx.ContinueAsNew(input+1, task.WithKeepUnprocessedEvents())
		return nil, nil
	})

	// Initialization
	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			// Run the orchestration
			id, err := client.ScheduleNewOrchestration(ctx, "ContinueAsNewTest", api.WithInput(0))
			require.NoError(t, err)
			for i := 0; i < 10; i++ {
				require.NoError(t, client.RaiseEvent(ctx, id, "MyEvent", api.WithEventPayload(false)))
			}
			require.NoError(t, client.RaiseEvent(ctx, id, "MyEvent", api.WithEventPayload(true)))
			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `10`, metadata.SerializedOutput)
		})
	}
}

// TestPurge verifies that a workflow can have a series of activities created and then
// verifies that all the metadata for those activities can be deleted from the statestore
func TestPurge(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("ActivityChainToPurge", func(ctx *task.OrchestrationContext) (any, error) {
		val := 0
		for i := 0; i < 10; i++ {
			if err := ctx.CallActivity("PlusOne", task.WithActivityInput(val)).Await(&val); err != nil {
				return nil, err
			}
		}
		return val, nil
	})
	r.AddActivityN("PlusOne", func(ctx task.ActivityContext) (any, error) {
		var input int
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return input + 1, nil
	})

	ctx := context.Background()
	client, engine, stateStore := startEngineAndGetStore(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ActivityChainToPurge")
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, id, metadata.InstanceID)

			// Get the number of keys that were stored from the activity and ensure that at least some keys were stored
			keyCounter := 0
			for key := range stateStore.Items {
				if strings.Contains(key, string(id)) {
					keyCounter += 1
				}
			}
			assert.Greater(t, keyCounter, 10)

			err = client.PurgeOrchestrationState(ctx, id)
			assert.NoError(t, err)

			// Check that no key from the statestore containing the actor id is still present in the statestore
			keysPostPurge := []string{}
			for key := range stateStore.Items {
				keysPostPurge = append(keysPostPurge, key)
			}

			for _, item := range keysPostPurge {
				if strings.Contains(item, string(id)) {
					assert.True(t, false)
				}
			}
		})
	}
}

func TestPurgeContinueAsNew(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("ContinueAsNewTest", func(ctx *task.OrchestrationContext) (any, error) {
		var input int32
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}

		if input < 10 {
			if err := ctx.CreateTimer(0).Await(nil); err != nil {
				return nil, err
			}
			var nextInput int32
			if err := ctx.CallActivity("PlusOne", task.WithActivityInput(input)).Await(&nextInput); err != nil {
				return nil, err
			}
			ctx.ContinueAsNew(nextInput)
		}
		return input, nil
	})
	r.AddActivityN("PlusOne", func(ctx task.ActivityContext) (any, error) {
		var input int32
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		return input + 1, nil
	})
	ctx := context.Background()
	client, engine, stateStore := startEngineAndGetStore(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ContinueAsNewTest", api.WithInput(0))
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `10`, metadata.SerializedOutput)

			// Purging
			// Get the number of keys that were stored from the activity and ensure that at least some keys were stored
			keyCounter := 0
			for key := range stateStore.Items {
				if strings.Contains(key, string(id)) {
					keyCounter += 1
				}
			}
			assert.Greater(t, keyCounter, 2)

			err = client.PurgeOrchestrationState(ctx, id)
			assert.NoError(t, err)

			// Check that no key from the statestore containing the actor id is still present in the statestore
			keysPostPurge := []string{}
			for key := range stateStore.Items {
				keysPostPurge = append(keysPostPurge, key)
			}

			for _, item := range keysPostPurge {
				if strings.Contains(item, string(id)) {
					assert.True(t, false)
				}
			}
		})
	}
}

func TestPauseResumeWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("PauseWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		if err := ctx.WaitForSingleEvent("WaitForThisEvent", 30*time.Second).Await(nil); err != nil {
			// Timeout expired
			return nil, err
		}
		return nil, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "PauseWorkflow")
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationStart(ctx, id)
				if assert.NoError(t, err) {
					assert.Equal(t, id, metadata.InstanceID)
					client.SuspendOrchestration(ctx, id, "PauseWFReasonTest")
					client.RaiseEvent(ctx, id, "WaitForThisEvent")
					assert.True(t, metadata.IsRunning())
					client.ResumeOrchestration(ctx, id, "ResumeWFReasonTest")
					metadata, _ = client.WaitForOrchestrationCompletion(ctx, id)
					assert.True(t, metadata.IsComplete())
					assert.Nil(t, metadata.FailureDetails)
				}
			}
		})
	}
}

func startEngine(ctx context.Context, t *testing.T, r *task.TaskRegistry) (backend.TaskHubClient, *wfengine.WorkflowEngine) {
	client, engine, _ := startEngineAndGetStore(ctx, t, r)
	return client, engine
}

func startEngineAndGetStore(ctx context.Context, t *testing.T, r *task.TaskRegistry) (backend.TaskHubClient, *wfengine.WorkflowEngine, *daprt.FakeStateStore) {
	var client backend.TaskHubClient
	engine, store := getEngineAndStateStore(t)
	engine.SetExecutor(func(be backend.Backend) backend.Executor {
		client = backend.NewTaskHubClient(be)
		return task.NewTaskExecutor(r)
	})
	require.NoError(t, engine.Start(ctx))
	return client, engine, store
}

func getEngine(t *testing.T) *wfengine.WorkflowEngine {
	engine := wfengine.NewWorkflowEngine(wfengine.NewWorkflowConfig(testAppID))
	store := fakeStore()
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:              testAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})
	compStore := compstore.New()
	compStore.AddStateStore("workflowStore", store)
	actors := actors.NewActors(actors.ActorsOpts{
		CompStore:      compStore,
		Config:         cfg,
		StateStoreName: "workflowStore",
		MockPlacement:  NewMockPlacement(),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})

	if err := actors.Init(); err != nil {
		require.NoError(t, err)
	}
	engine.SetActorRuntime(actors)
	return engine
}

func getEngineAndStateStore(t *testing.T) (*wfengine.WorkflowEngine, *daprt.FakeStateStore) {
	engine := wfengine.NewWorkflowEngine(wfengine.NewWorkflowConfig(testAppID))
	store := fakeStore().(*daprt.FakeStateStore)
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:              testAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})
	compStore := compstore.New()
	compStore.AddStateStore("workflowStore", store)

	actors := actors.NewActors(actors.ActorsOpts{
		CompStore:      compStore,
		Config:         cfg,
		StateStoreName: "workflowStore",
		MockPlacement:  NewMockPlacement(),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})

	require.NoError(t, actors.Init())
	engine.SetActorRuntime(actors)
	return engine, store
}
