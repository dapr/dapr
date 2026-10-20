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
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
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
// succeeds, and every workflow completes, exactly once, once a worker
// connects again.
type transition struct {
	workflow *workflow.Workflow

	lock        sync.Mutex
	completions map[api.InstanceID]int
}

const transitionDamping = 2 * time.Second

func (tr *transition) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	tr.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithFallbackHosts(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID), withDamping(t, transitionDamping)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), withDamping(t, transitionDamping)),
	)
	tr.completions = make(map[api.InstanceID]int)

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
			tr.completions[ctx.ID]++
			tr.lock.Unlock()
		}
		return ev, nil
	}))

	mgmt := []*client.TaskHubGrpcClient{
		tr.workflow.ManagementClientN(t, ctx, 0),
		tr.workflow.ManagementClientN(t, ctx, 1),
	}

	// Both daprds are fallback hosts once the placement tables arrive.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hostsWorkflows(c, ctx, tr.workflow, 0))
		assert.True(c, hostsWorkflows(c, ctx, tr.workflow, 1))
	}, time.Second*10, time.Millisecond*50)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var idsLock sync.Mutex
	var ids []api.InstanceID
	for g := range 4 {
		m := mgmt[g%2]
		wg.Go(func() {
			for n := 0; ; n++ {
				select {
				case <-stop:
					return
				default:
				}

				// A start time skips the executor's wait for the instance to
				// start, which a daprd with a worker makes, as the worker may
				// be gone before the instance starts.
				octx, cancel := context.WithTimeout(ctx, time.Second*10)
				id := api.InstanceID(fmt.Sprintf("fallback-transition-%d-%d", g, n))
				_, err := m.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(id), api.WithStartTime(time.Now()))
				if assert.NoError(t, err, "start %s", id) {
					assert.NoError(t, m.RaiseEvent(octx, id, "go", api.WithEventPayload(string(id))), "raise %s", id)
					idsLock.Lock()
					ids = append(ids, id)
					idsLock.Unlock()
				}
				cancel()
				time.Sleep(time.Millisecond * 20)
			}
		})
	}

	// No worker anywhere.
	time.Sleep(time.Second)

	// The first worker connects; the other daprd gives the types up after
	// the damping period.
	worker := tr.workflow.ConnectWorkerN(t, ctx, 0, tr.workflow.RegistryN(0))
	tr.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, hostsWorkflows(c, ctx, tr.workflow, 1))
	}, time.Second*20, time.Millisecond*50)
	time.Sleep(time.Second)

	// The last worker leaves: both daprds are fallback hosts again.
	worker.Disconnect(t)
	tr.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hostsWorkflows(c, ctx, tr.workflow, 1))
	}, time.Second*10, time.Millisecond*50)
	time.Sleep(time.Second)

	close(stop)
	wg.Wait()
	require.NotEmpty(t, ids)

	tr.workflow.ConnectWorkerN(t, ctx, 0, tr.workflow.RegistryN(0))
	for _, id := range ids {
		meta, err := mgmt[0].WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
		assert.Equal(t, fmt.Sprintf("%q", id), meta.GetOutput().GetValue(), id)
	}

	tr.lock.Lock()
	defer tr.lock.Unlock()
	assert.Len(t, tr.completions, len(ids))
	for _, id := range ids {
		assert.Equal(t, 1, tr.completions[id], id)
	}
}
