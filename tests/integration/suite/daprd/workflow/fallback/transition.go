/*
Copyright 2026 The Dapr Authors
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

package fallback

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	fworkflow "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(transition))
}

// transition starts workflows and raises their events in a loop, through
// both daprds of an app, while the app goes from no worker to a worker
// connecting to one daprd, past the damping period after which the other
// gives the workflow actor types up, and back to no worker. Every call
// succeeds, and every workflow completes, with a single completion in its
// history, once a worker connects again. A workflow runs at least once: a
// turn whose commit a worker's leave cut short runs again, not replaying.
type transition struct {
	workflow *workflow.Workflow

	lock sync.Mutex
	runs map[api.InstanceID]int
}

const transitionDamping = 2 * time.Second

func (tr *transition) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	tr.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithStagedOperations(),
		withSettle(t, 0),
		withSettle(t, 1),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), daprd.WithWorkflowFallbackDamping(t, transitionDamping)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), daprd.WithWorkflowFallbackDamping(t, transitionDamping)),
	)
	tr.runs = make(map[api.InstanceID]int)

	return []framework.Option{
		framework.WithProcesses(tr.workflow),
	}
}

func (tr *transition) Run(t *testing.T, ctx context.Context) {
	tr.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, tr.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		var ev string
		if err := ctx.WaitForSingleEvent("go", time.Hour).Await(&ev); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			tr.lock.Lock()
			tr.runs[ctx.ID]++
			tr.lock.Unlock()
		}
		return ev, nil
	}))

	mgmt := []*client.TaskHubGrpcClient{
		tr.workflow.ManagementClientN(t, ctx, 0),
		tr.workflow.ManagementClientN(t, ctx, 1),
	}

	// Both daprds are fallback hosts once the placement tables have shown no
	// executor for the settle period.
	hosts := func(c assert.TestingT, index int) bool {
		return tr.workflow.HostsActorType(c, ctx, index, tr.workflow.WorkflowActorType(index))
	}
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hosts(c, 0))
		assert.True(c, hosts(c, 1))
	}, time.Second*10, time.Millisecond*10)

	// Goroutines 0 and 2 start instances through daprd 0, 1 and 3 through
	// daprd 1.
	load := workflow.StartConcurrently(t, 4, func(g, n int) (api.InstanceID, bool) {
		m := mgmt[g%2]

		// A start time skips the executor's wait for the instance to
		// start, which a daprd with a worker makes, as the worker may
		// be gone before the instance starts.
		octx, cancel := context.WithTimeout(ctx, time.Second*10)
		defer time.Sleep(time.Millisecond * 20)
		defer cancel()
		id := api.InstanceID(fmt.Sprintf("fallback-transition-%d-%d", g, n))
		_, err := m.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(id), api.WithStartTime(time.Now()))
		if !assert.NoError(t, err, "start %s", id) {
			return "", false
		}
		assert.NoError(t, m.RaiseEvent(octx, id, "go", api.WithEventPayload(string(id))), "raise %s", id)
		return id, true
	})

	// No worker anywhere: both daprds are fallback hosts while the load runs
	// through each.
	load.WaitStarted(t, 2)
	assert.True(t, hosts(t, 0))
	assert.True(t, hosts(t, 1))

	// The first worker connects; the other daprd gives the types up after
	// the damping period, and the load runs against the daprd with the
	// worker alone.
	worker := tr.workflow.ConnectWorkerN(t, ctx, 0, tr.workflow.RegistryN(0))
	tr.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, hosts(c, 1))
	}, time.Second*20, time.Millisecond*10)
	load.WaitStarted(t, 2)
	assert.True(t, hosts(t, 0))
	assert.False(t, hosts(t, 1))

	// The last worker leaves: both daprds are fallback hosts again, the one
	// it left at once, the other after the settle period.
	worker.Disconnect(t)
	tr.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hosts(c, 1))
	}, time.Second*10, time.Millisecond*10)
	load.WaitStarted(t, 2)
	assert.True(t, hosts(t, 0))
	assert.True(t, hosts(t, 1))

	ids := load.Stop()
	require.NotEmpty(t, ids)

	tr.workflow.ConnectWorkerN(t, ctx, 0, tr.workflow.RegistryN(0))
	for _, id := range ids {
		meta := fworkflow.WaitForCompleted(t, ctx, mgmt[0], id)
		assert.Equal(t, fmt.Sprintf("%q", id), meta.GetOutput().GetValue(), id)

		hist, err := mgmt[0].GetInstanceHistory(ctx, id)
		require.NoError(t, err, id)
		var completed int
		for _, e := range hist.GetEvents() {
			if e.GetExecutionCompleted() != nil {
				completed++
			}
		}
		assert.Equal(t, 1, completed, "completions in the history of %s", id)
	}

	tr.lock.Lock()
	defer tr.lock.Unlock()
	assert.Len(t, tr.runs, len(ids))
	for _, id := range ids {
		assert.GreaterOrEqual(t, tr.runs[id], 1, id)
	}
}
