//go:build unit
// +build unit

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
	"strconv"
	"strings"
	"sync/atomic"
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
	"github.com/dapr/dapr/pkg/runtime/processor"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	actorsbe "github.com/dapr/dapr/pkg/runtime/wfengine/backends/actors"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	testAppID         = "wf-app"
	workflowActorType = "dapr.internal.default.wf-app.workflow"
	activityActorType = "dapr.internal.default.wf-app.activity"
)

func fakeStore() state.Store {
	return daprt.NewFakeStateStore()
}

func init() {
	wfengine.SetLogLevel(logger.DebugLevel)
}

// TestStartWorkflowEngine validates that starting the workflow engine returns no errors.
func TestStartWorkflowEngine(t *testing.T) {
	ctx := context.Background()
	engine := getEngine(t, ctx)
	engine.ConfigureGrpcExecutor()
	grpcServer := grpc.NewServer()
	engine.RegisterGrpcServer(grpcServer)
	err := engine.Start(ctx)
	require.NoError(t, err)
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
			if abe, ok := wfe.Backend.(*actorsbe.ActorBackend); ok {
				abe.DisableActorCaching(true)
				return "caching disabled"
			}
			return ""
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

// TestSingleActivityWorkflow_ReuseInstanceIDIgnore executes a workflow twice.
// The workflow calls a single activity with orchestraion ID reuse policy.
// The reuse ID policy contains action IGNORE and target status ['RUNNING', 'COMPLETED', 'PENDING']
// The second call to create a workflow with same instance ID is expected to be ignored
// if first workflow instance is in above statuses
func TestSingleActivityWorkflow_ReuseInstanceIDIgnore(t *testing.T) {
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
		suffix := opt(engine)
		t.Run(suffix, func(t *testing.T) {
			instanceID := api.InstanceID("IGNORE_IF_RUNNING_OR_COMPLETED_" + suffix)
			reuseIDPolicy := &api.OrchestrationIdReusePolicy{
				Action:          api.REUSE_ID_ACTION_IGNORE,
				OperationStatus: []api.OrchestrationStatus{api.RUNTIME_STATUS_RUNNING, api.RUNTIME_STATUS_COMPLETED, api.RUNTIME_STATUS_PENDING},
			}

			// Run the orchestration
			id, err := client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("世界"), api.WithInstanceID(instanceID))
			require.NoError(t, err)
			// wait orchestration to start
			client.WaitForOrchestrationStart(ctx, id)
			pivotTime := time.Now()
			// schedule again, it should ignore creating the new orchestration
			id, err = client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("World"), api.WithInstanceID(id), api.WithOrchestrationIdReusePolicy(reuseIDPolicy))
			require.NoError(t, err)
			timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
			defer cancelTimeout()
			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			// the first orchestration should complete as the second one is ignored
			assert.Equal(t, `"Hello, 世界!"`, metadata.SerializedOutput)
			// assert the orchestration created timestamp
			assert.True(t, pivotTime.After(metadata.CreatedAt))
		})
	}
}

// TestSingleActivityWorkflow_ReuseInstanceIDIgnore executes a workflow twice.
// The workflow calls a single activity with orchestraion ID reuse policy.
// The reuse ID policy contains action TERMINATE and target status ['RUNNING', 'COMPLETED', 'PENDING']
// The second call to create a workflow with same instance ID is expected to terminate
// the first workflow instance and create a new workflow instance if it's status is in target statuses
func TestSingleActivityWorkflow_ReuseInstanceIDTerminate(t *testing.T) {
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
		suffix := opt(engine)
		t.Run(suffix, func(t *testing.T) {
			instanceID := api.InstanceID("IGNORE_IF_RUNNING_OR_COMPLETED_" + suffix)
			reuseIDPolicy := &api.OrchestrationIdReusePolicy{
				Action:          api.REUSE_ID_ACTION_TERMINATE,
				OperationStatus: []api.OrchestrationStatus{api.RUNTIME_STATUS_RUNNING, api.RUNTIME_STATUS_COMPLETED, api.RUNTIME_STATUS_PENDING},
			}

			// Run the orchestration
			id, err := client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("世界"), api.WithInstanceID(instanceID))
			require.NoError(t, err)
			// wait orchestration to start
			client.WaitForOrchestrationStart(ctx, id)
			pivotTime := time.Now()
			// schedule again, it should ignore creating the new orchestration
			id, err = client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("World"), api.WithInstanceID(id), api.WithOrchestrationIdReusePolicy(reuseIDPolicy))
			require.NoError(t, err)
			timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
			defer cancelTimeout()
			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			// the first orchestration should complete as the second one is ignored
			assert.Equal(t, `"Hello, World!"`, metadata.SerializedOutput)
			// assert the orchestration created timestamp
			assert.False(t, pivotTime.After(metadata.CreatedAt))
		})
	}
}

// TestSingleActivityWorkflow_ReuseInstanceIDIgnore executes a workflow twice.
// The workflow calls a single activity with orchestraion ID reuse policy.
// The reuse ID policy contains default action Error and empty target status
// The second call to create a workflow with same instance ID is expected to error out
// the first workflow instance is not completed.
func TestSingleActivityWorkflow_ReuseInstanceIDError(t *testing.T) {
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
		suffix := opt(engine)
		t.Run(suffix, func(t *testing.T) {
			instanceID := api.InstanceID("IGNORE_IF_RUNNING_OR_COMPLETED_" + suffix)
			id, err := client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("世界"), api.WithInstanceID(instanceID))
			require.NoError(t, err)
			_, err = client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("World"), api.WithInstanceID(id))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "already exists")
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
			require.NoError(t, err)

			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)

			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `10`, metadata.SerializedOutput)
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
		return strconv.Itoa(input), nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ActivityFanOut")
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)

			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `["9","8","7","6","5","4","3","2","1","0"]`, metadata.SerializedOutput)

			// Because all the activities run in parallel, they should complete very quickly
			assert.Less(t, metadata.LastUpdatedAt.Sub(metadata.CreatedAt), 3*time.Second)
		})
	}
}

// TestChildWorkflow creates a workflow that calls a child workflow and verifies that the child workflow
// completes successfully.
func TestChildWorkflow(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("root", func(ctx *task.OrchestrationContext) (any, error) {
		var input string
		if err := ctx.GetInput(&input); err != nil {
			return nil, err
		}
		var output string
		err := ctx.CallSubOrchestrator("child", task.WithSubOrchestratorInput(input)).Await(&output)
		return output, err
	})
	r.AddOrchestratorN("child", func(ctx *task.OrchestrationContext) (any, error) {
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
			// Run the root orchestration
			id, err := client.ScheduleNewOrchestration(ctx, "root", api.WithInput("世界"))
			require.NoError(t, err)
			timeoutCtx, cancelTimeout := context.WithTimeout(ctx, 30*time.Second)
			defer cancelTimeout()
			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `"Hello, 世界!"`, metadata.SerializedOutput)
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
			require.NoError(t, err)
			metadata, err = client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `"echo!"`, metadata.SerializedOutput)

			// Second workflow, using the same ID as the first but a different input
			_, err = client.ScheduleNewOrchestration(ctx, "EchoWorkflow", api.WithInstanceID(id), api.WithInput(42))
			require.NoError(t, err)
			metadata, err = client.WaitForOrchestrationCompletion(ctx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `42`, metadata.SerializedOutput)
		})
	}
}

func TestInternalActorsSetupForWF(t *testing.T) {
	ctx := context.Background()
	_, engine := startEngine(ctx, t, task.NewTaskRegistry())
	abe, ok := engine.Backend.(*actorsbe.ActorBackend)

	assert.True(t, ok, "engine.Backend is of type ActorBackend")
	assert.Len(t, abe.GetInternalActorsMap(), 2)
	assert.Contains(t, abe.GetInternalActorsMap(), workflowActorType)
	assert.Contains(t, abe.GetInternalActorsMap(), activityActorType)
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
			require.NoError(t, err)
			metadata, err = client.WaitForOrchestrationStart(ctx, id)
			require.NoError(t, err)
			assert.False(t, metadata.IsComplete())

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
	actualCallCount := atomic.Int32{}

	r := task.NewTaskRegistry()
	r.AddOrchestratorN("FlakyWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		// update this global counter each time the workflow gets invoked
		acc := actualCallCount.Add(1)
		if acc < expectedCallCount {
			// simulate a hang for the first two calls
			time.Sleep(5 * time.Minute)
		}
		return acc, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)

	abe, _ := engine.Backend.(*actorsbe.ActorBackend)

	// Set a really short timeout to override the default workflow timeout so that we can exercise the timeout
	// handling codepath in a short period of time.
	abe.SetWorkflowTimeout(1 * time.Second)

	// Set a really short reminder interval to retry workflows immediately after they time out.
	abe.SetActorReminderInterval(1 * time.Millisecond)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			actualCallCount.Store(0)

			id, err := client.ScheduleNewOrchestration(ctx, "FlakyWorkflow")
			require.NoError(t, err)
			// Add a 5 second timeout so that the test doesn't take forever if something isn't working
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, strconv.Itoa(expectedCallCount), metadata.SerializedOutput)
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
	actualCallCount := atomic.Int32{}

	r.AddActivityN("FlakyActivity", func(ctx task.ActivityContext) (any, error) {
		// update this global counter each time the activity gets invoked
		acc := actualCallCount.Add(1)
		if acc < expectedCallCount {
			// simulate a hang for the first two calls
			time.Sleep(5 * time.Minute)
		}
		return acc, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, t, r)

	abe, _ := engine.Backend.(*actorsbe.ActorBackend)

	// Set a really short timeout to override the default activity timeout (1 hour at the time of writing)
	// so that we can exercise the timeout handling codepath in a short period of time.
	abe.SetActivityTimeout(1 * time.Second)

	// Set a really short reminder interval to retry activities immediately after they time out.
	abe.SetActorReminderInterval(1 * time.Millisecond)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			actualCallCount.Store(0)

			id, err := client.ScheduleNewOrchestration(ctx, "FlakyWorkflow")
			require.NoError(t, err)
			// Add a 5 second timeout so that the test doesn't take forever if something isn't working
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, strconv.Itoa(expectedCallCount), metadata.SerializedOutput)
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
			require.NoError(t, err)
			// Add a 5 second timeout so that the test doesn't take forever if something isn't working
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
			require.NoError(t, err)
			assert.True(t, metadata.IsComplete())

			// Because all the timers run in parallel, they should complete very quickly
			assert.Less(t, metadata.LastUpdatedAt.Sub(metadata.CreatedAt), 3*time.Second)
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
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationStart(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, id, metadata.InstanceID)
			client.RaiseEvent(ctx, id, "NameOfEventBeingRaised", api.WithEventPayload("NameOfInput"))
			metadata, _ = client.WaitForOrchestrationCompletion(ctx, id)
			assert.True(t, metadata.IsComplete())
			assert.Equal(t, `"Hello, NameOfInput!"`, metadata.SerializedOutput)
			assert.Nil(t, metadata.FailureDetails)
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
			keyCounter := atomic.Int64{}
			for key := range stateStore.GetItems() {
				if strings.Contains(key, string(id)) {
					keyCounter.Add(1)
				}
			}
			assert.Greater(t, keyCounter.Load(), int64(10))

			err = client.PurgeOrchestrationState(ctx, id)
			require.NoError(t, err)

			// Check that no key from the statestore containing the actor id is still present in the statestore
			keysPostPurge := []string{}
			for key := range stateStore.GetItems() {
				keysPostPurge = append(keysPostPurge, key)
			}

			for _, item := range keysPostPurge {
				if strings.Contains(item, string(id)) {
					assert.Truef(t, false, "Found key post-purge that should not have existed: %v", item)
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
			keyCounter := atomic.Int64{}
			for key := range stateStore.GetItems() {
				if strings.Contains(key, string(id)) {
					keyCounter.Add(1)
				}
			}
			assert.Greater(t, keyCounter.Load(), int64(2))

			err = client.PurgeOrchestrationState(ctx, id)
			require.NoError(t, err)

			// Check that no key from the statestore containing the actor id is still present in the statestore
			keysPostPurge := []string{}
			for key := range stateStore.GetItems() {
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
			require.NoError(t, err)
			metadata, err := client.WaitForOrchestrationStart(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, id, metadata.InstanceID)
			client.SuspendOrchestration(ctx, id, "PauseWFReasonTest")
			client.RaiseEvent(ctx, id, "WaitForThisEvent")
			assert.True(t, metadata.IsRunning())
			client.ResumeOrchestration(ctx, id, "ResumeWFReasonTest")
			metadata, _ = client.WaitForOrchestrationCompletion(ctx, id)
			assert.True(t, metadata.IsComplete())
			assert.Nil(t, metadata.FailureDetails)
		})
	}
}

func startEngine(ctx context.Context, t *testing.T, r *task.TaskRegistry) (backend.TaskHubClient, *wfengine.WorkflowEngine) {
	client, engine, _ := startEngineAndGetStore(ctx, t, r)
	return client, engine
}

func startEngineAndGetStore(ctx context.Context, t *testing.T, r *task.TaskRegistry) (backend.TaskHubClient, *wfengine.WorkflowEngine, *daprt.FakeStateStore) {
	var client backend.TaskHubClient
	engine, store := getEngineAndStateStore(t, ctx)
	engine.SetExecutor(func(be backend.Backend) backend.Executor {
		client = backend.NewTaskHubClient(be)
		return task.NewTaskExecutor(r)
	})
	require.NoError(t, engine.Start(ctx))
	return client, engine, store
}

func getEngine(t *testing.T, ctx context.Context) *wfengine.WorkflowEngine {
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}

	processor := processor.New(processor.Options{
		Registry:     registry.New(registry.NewOptions()),
		GlobalConfig: new(config.Configuration),
	})

	engine := wfengine.NewWorkflowEngine(testAppID, spec, processor.WorkflowBackend())
	store := fakeStore()
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:         testAppID,
		ActorsService: "placement:placement:5050",
		AppConfig:     config.ApplicationConfig{},
	})
	compStore := compstore.New()
	compStore.AddStateStore("workflowStore", store)
	actors, err := actors.NewActors(actors.ActorsOpts{
		CompStore:      compStore,
		Config:         cfg,
		StateStoreName: "workflowStore",
		MockPlacement:  actors.NewMockPlacement(testAppID),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})
	require.NoError(t, err)

	if err := actors.Init(context.Background()); err != nil {
		require.NoError(t, err)
	}
	abe, _ := engine.Backend.(*actorsbe.ActorBackend)
	abe.SetActorRuntime(ctx, actors)
	return engine
}

func getEngineAndStateStore(t *testing.T, ctx context.Context) (*wfengine.WorkflowEngine, *daprt.FakeStateStore) {
	spec := config.WorkflowSpec{MaxConcurrentWorkflowInvocations: 100, MaxConcurrentActivityInvocations: 100}
	processor := processor.New(processor.Options{
		Registry:     registry.New(registry.NewOptions()),
		GlobalConfig: new(config.Configuration),
	})
	engine := wfengine.NewWorkflowEngine(testAppID, spec, processor.WorkflowBackend())
	store := fakeStore().(*daprt.FakeStateStore)
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:         testAppID,
		ActorsService: "placement:placement:5050",
		AppConfig:     config.ApplicationConfig{},
		HostAddress:   "localhost",
		Port:          5000, // port for unit tests to pass IsLocalActor
	})
	compStore := compstore.New()
	compStore.AddStateStore("workflowStore", store)

	actors, err := actors.NewActors(actors.ActorsOpts{
		CompStore:      compStore,
		Config:         cfg,
		StateStoreName: "workflowStore",
		MockPlacement:  actors.NewMockPlacement(testAppID),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})
	require.NoError(t, err)

	require.NoError(t, actors.Init(context.Background()))
	abe, _ := engine.Backend.(*actorsbe.ActorBackend)
	abe.SetActorRuntime(ctx, actors)
	return engine, store
}
