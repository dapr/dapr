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
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/durabletask-go/api"
	"github.com/microsoft/durabletask-go/backend"
	"github.com/microsoft/durabletask-go/task"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/dapr/components-contrib/state"
	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/wfengine"
	"github.com/dapr/kit/logger"
)

const testAppID = "wf-app"

// The fake state store code was copied from actors_test.go.
// TODO: Find a way to share the code instead of copying it, if it makes sense to do so.

type fakeStateStoreItem struct {
	data []byte
	etag *string
}

type fakeStateStore struct {
	items map[string]*fakeStateStoreItem
	lock  *sync.RWMutex
}

func fakeStore() state.Store {
	return &fakeStateStore{
		items: map[string]*fakeStateStoreItem{},
		lock:  &sync.RWMutex{},
	}
}

func (f *fakeStateStore) newItem(data []byte) *fakeStateStoreItem {
	etag, _ := uuid.NewRandom()
	etagString := etag.String()
	return &fakeStateStoreItem{
		data: data,
		etag: &etagString,
	}
}

func (f *fakeStateStore) Init(metadata state.Metadata) error {
	return nil
}

func (f *fakeStateStore) Ping() error {
	return nil
}

func (f *fakeStateStore) Features() []state.Feature {
	return []state.Feature{state.FeatureETag, state.FeatureTransactional}
}

func (f *fakeStateStore) Delete(ctx context.Context, req *state.DeleteRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	delete(f.items, req.Key)

	return nil
}

func (f *fakeStateStore) BulkDelete(ctx context.Context, req []state.DeleteRequest) error {
	return nil
}

func (f *fakeStateStore) Get(ctx context.Context, req *state.GetRequest) (*state.GetResponse, error) {
	f.lock.RLock()
	defer f.lock.RUnlock()
	item := f.items[req.Key]

	if item == nil {
		return &state.GetResponse{Data: nil, ETag: nil}, nil
	}

	return &state.GetResponse{Data: item.data, ETag: item.etag}, nil
}

func (f *fakeStateStore) BulkGet(ctx context.Context, req []state.GetRequest) (bool, []state.BulkGetResponse, error) {
	res := []state.BulkGetResponse{}
	for _, oneRequest := range req {
		oneResponse, err := f.Get(ctx, &state.GetRequest{
			Key:      oneRequest.Key,
			Metadata: oneRequest.Metadata,
			Options:  oneRequest.Options,
		})
		if err != nil {
			return false, nil, err
		}

		res = append(res, state.BulkGetResponse{
			Key:  oneRequest.Key,
			Data: oneResponse.Data,
			ETag: oneResponse.ETag,
		})
	}

	return true, res, nil
}

func (f *fakeStateStore) Set(ctx context.Context, req *state.SetRequest) error {
	b, _ := marshal(&req.Value, json.Marshal)
	f.lock.Lock()
	defer f.lock.Unlock()
	f.items[req.Key] = f.newItem(b)

	return nil
}

func (f *fakeStateStore) GetComponentMetadata() map[string]string {
	return map[string]string{}
}

func (f *fakeStateStore) BulkSet(ctx context.Context, req []state.SetRequest) error {
	return nil
}

func (f *fakeStateStore) Multi(ctx context.Context, request *state.TransactionalStateRequest) error {
	f.lock.Lock()
	defer f.lock.Unlock()
	// First we check all eTags
	for _, o := range request.Operations {
		var eTag *string
		key := ""
		if o.Operation == state.Upsert {
			key = o.Request.(state.SetRequest).Key
			eTag = o.Request.(state.SetRequest).ETag
		} else if o.Operation == state.Delete {
			key = o.Request.(state.DeleteRequest).Key
			eTag = o.Request.(state.DeleteRequest).ETag
		}
		item := f.items[key]
		if eTag != nil && item != nil {
			if *eTag != *item.etag {
				return fmt.Errorf("etag does not match for key %v", key)
			}
		}
		if eTag != nil && item == nil {
			return fmt.Errorf("etag does not match for key not found %v", key)
		}
	}

	// Now we can perform the operation.
	for _, o := range request.Operations {
		if o.Operation == state.Upsert {
			req := o.Request.(state.SetRequest)
			b, _ := marshal(req.Value, json.Marshal)
			f.items[req.Key] = f.newItem(b)
		} else if o.Operation == state.Delete {
			req := o.Request.(state.DeleteRequest)
			delete(f.items, req.Key)
		}
	}

	return nil
}

// Copied from https://github.com/dapr/components-contrib/blob/a4b27ae49b7c99820c6e921d3891f03334692714/state/utils/utils.go#L16
func marshal(val interface{}, marshaler func(interface{}) ([]byte, error)) ([]byte, error) {
	var err error = nil
	bt, ok := val.([]byte)
	if !ok {
		bt, err = marshaler(val)
	}

	return bt, err
}

type mockPlacement struct{}

func NewMockPlacement() actors.PlacementService {
	return &mockPlacement{}
}

// LookupActor implements internal.PlacementService
func (*mockPlacement) LookupActor(actorType string, actorID string) (name string, appID string) {
	return "localhost", testAppID
}

// Start implements internal.PlacementService
func (*mockPlacement) Start() {
	// no-op
}

// Stop implements internal.PlacementService
func (*mockPlacement) Stop() {
	// no-op
}

// WaitUntilPlacementTableIsReady implements internal.PlacementService
func (*mockPlacement) WaitUntilPlacementTableIsReady(ctx context.Context) error {
	return nil
}

// TestStartWorkflowEngine validates that starting the workflow engine returns no errors.
func TestStartWorkflowEngine(t *testing.T) {
	ctx := context.Background()
	engine := getEngine()
	grpcServer := grpc.NewServer()
	engine.ConfigureGrpc(grpcServer)
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
	client, engine := startEngine(ctx, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			preStartTime := time.Now().UTC()
			id, err := client.ScheduleNewOrchestration(ctx, "EmptyWorkflow")
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
				if assert.NoError(t, err) {
					assert.Equal(t, id, metadata.InstanceID)
					assert.True(t, metadata.IsComplete())
					assert.GreaterOrEqual(t, metadata.CreatedAt, preStartTime)
					assert.GreaterOrEqual(t, metadata.LastUpdatedAt, metadata.CreatedAt)
					assert.Empty(t, metadata.SerializedInput)
					assert.Empty(t, metadata.SerializedOutput)
					assert.Empty(t, metadata.SerializedCustomStatus)
					assert.Nil(t, metadata.FailureDetails)
				}
			}
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
	client, engine := startEngine(ctx, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "SingleTimer")
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.GreaterOrEqual(t, metadata.LastUpdatedAt, metadata.CreatedAt)
				}
			}
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
		err := ctx.CallActivity("SayHello", input).Await(&output)
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
	client, engine := startEngine(ctx, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "SingleActivity", api.WithInput("世界"))
			if assert.NoError(t, err) {
				metadata, err := client.WaitForOrchestrationCompletion(ctx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, `"世界"`, metadata.SerializedInput)
					assert.Equal(t, `"Hello, 世界!"`, metadata.SerializedOutput)
				}
			}
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
			if err := ctx.CallActivity("PlusOne", val).Await(&val); err != nil {
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
	client, engine := startEngine(ctx, r)
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
			tasks = append(tasks, ctx.CallActivity("ToString", i))
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
	client, engine := startEngine(ctx, r)
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
			ctx.ContinueAsNew(input + 1)
		}
		return input, nil
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, r)
	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			id, err := client.ScheduleNewOrchestration(ctx, "ContinueAsNewTest", api.WithInput(0))
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
	client, engine := startEngine(ctx, r)
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

// TestRecreateRunningWorkflowFails verifies that a workflow can't be recreated if it's in a running state.
func TestRecreateRunningWorkflowFails(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("SleepyWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		err := ctx.CreateTimer(24 * time.Hour).Await(nil)
		return nil, err
	})

	ctx := context.Background()
	client, engine := startEngine(ctx, r)
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
			if assert.Error(t, err) {
				// We expect that the workflow instance ID is included in the error message
				assert.Contains(t, err.Error(), id)
			}
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
	client, engine := startEngine(ctx, r)

	// Set a really short timeout to override the default workflow timeout so that we can exercise the timeout
	// handling codepath in a short period of time.
	engine.SetWorkflowTimeout(1 * time.Second)

	// Set a really short reminder interval to retry workflows immediately after they time out.
	engine.SetActorReminderInterval(1 * time.Millisecond)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			actualCallCount = 0

			id, err := client.ScheduleNewOrchestration(ctx, "FlakyWorkflow")
			if assert.NoError(t, err) {
				// Add a 5 second timeout so that the test doesn't take forever if something isn't working
				timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, fmt.Sprintf("%d", expectedCallCount), metadata.SerializedOutput)
				}
			}
		})
	}
}

// TestRetryActivityOnTimeout verifies that activity operations are retried when they fail to complete.
func TestRetryActivityOnTimeout(t *testing.T) {
	r := task.NewTaskRegistry()
	r.AddOrchestratorN("FlakyWorkflow", func(ctx *task.OrchestrationContext) (any, error) {
		var output int
		err := ctx.CallActivity("FlakyActivity", nil).Await(&output)
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
	client, engine := startEngine(ctx, r)

	// Set a really short timeout to override the default activity timeout (1 hour at the time of writing)
	// so that we can exercise the timeout handling codepath in a short period of time.
	engine.SetActivityTimeout(1 * time.Second)

	// Set a really short reminder interval to retry activities immediately after they time out.
	engine.SetActorReminderInterval(1 * time.Millisecond)

	for _, opt := range GetTestOptions() {
		t.Run(opt(engine), func(t *testing.T) {
			actualCallCount = 0

			id, err := client.ScheduleNewOrchestration(ctx, "FlakyWorkflow")
			if assert.NoError(t, err) {
				// Add a 5 second timeout so that the test doesn't take forever if something isn't working
				timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()

				metadata, err := client.WaitForOrchestrationCompletion(timeoutCtx, id)
				if assert.NoError(t, err) {
					assert.True(t, metadata.IsComplete())
					assert.Equal(t, fmt.Sprintf("%d", expectedCallCount), metadata.SerializedOutput)
				}
			}
		})
	}
}

func startEngine(ctx context.Context, r *task.TaskRegistry) (backend.TaskHubClient, *wfengine.WorkflowEngine) {
	var client backend.TaskHubClient
	engine := getEngine()
	engine.ConfigureExecutor(func(be backend.Backend) backend.Executor {
		client = backend.NewTaskHubClient(be)
		return task.NewTaskExecutor(r)
	})
	if err := engine.Start(ctx); err != nil {
		panic(err)
	}
	return client, engine
}

func getEngine() *wfengine.WorkflowEngine {
	engine := wfengine.NewWorkflowEngine()
	store := fakeStore()
	cfg := actors.NewConfig(actors.ConfigOpts{
		AppID:              testAppID,
		PlacementAddresses: []string{"placement:5050"},
		AppConfig:          config.ApplicationConfig{},
	})
	actors := actors.NewActors(actors.ActorsOpts{
		StateStore:     store,
		Config:         cfg,
		StateStoreName: "workflowStore",
		InternalActors: engine.InternalActors(),
		MockPlacement:  NewMockPlacement(),
		Resiliency:     resiliency.New(logger.NewLogger("test")),
	})

	if err := actors.Init(); err != nil {
		panic(err)
	}
	engine.SetActorRuntime(actors)
	return engine
}
