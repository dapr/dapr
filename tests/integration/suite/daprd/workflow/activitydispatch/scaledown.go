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

package activitydispatch

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(scaledown))
}

// scaledown asserts that removing a replica which is not running the pull
// activity does not stall it or later dispatch: the in-flight workflow
// completes on the remaining replica, and new work is dispatched only to the
// remaining replica because the dead one is no longer an eligible slot pool.
//
// The removed replica may have hosted the workflow actor. Its death can then
// re-drive the orchestrator turn on the survivor and re-run the activity,
// which is at-least-once behaviour shared with hashed dispatch, so releases
// are per workflow instance rather than counted.
type scaledown struct {
	workflow *workflow.Workflow
}

func (s *scaledown) Setup(t *testing.T) []framework.Option {
	const appID = "pull-scaledown"
	cfg := pullConfig("pullscaledown", 1)
	s.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
	)
	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *scaledown) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	// started[i][tag] counts activity executions of workflow instance tag on
	// replica i; release[tag] lets every execution of that instance finish.
	var mu sync.Mutex
	started := [2]map[string]int{{}, {}}
	release := map[string]chan struct{}{
		"first":  make(chan struct{}),
		"second": make(chan struct{}),
	}
	count := func(i int, tag string) int {
		mu.Lock()
		defer mu.Unlock()
		return started[i][tag]
	}

	var scheduledOn atomic.Int64
	for i := range 2 {
		s.workflow.RegistryN(i).AddWorkflowN("single", func(ctx *task.WorkflowContext) (any, error) {
			var tag string
			if err := ctx.GetInput(&tag); err != nil {
				return nil, err
			}
			return nil, ctx.CallActivity("slow", task.WithActivityInput(tag)).Await(nil)
		})
		s.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			var tag string
			if err := ctx.GetInput(&tag); err != nil {
				return nil, err
			}
			mu.Lock()
			started[i][tag]++
			mu.Unlock()
			scheduledOn.Store(int64(i))
			<-release[tag]
			return nil, nil
		})
	}

	clients := []*client.TaskHubGrpcClient{
		s.workflow.BackendClientN(t, ctx, 0),
		s.workflow.BackendClientN(t, ctx, 1),
	}

	id, err := clients[0].ScheduleNewWorkflow(ctx, "single", api.WithInput("first"), api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, 1, count(0, "first")+count(1, "first"))
	}, time.Second*20, time.Millisecond*10)
	host := int(scheduledOn.Load())
	idle := 1 - host

	// Remove the replica that is not running the activity.
	s.workflow.DaprN(idle).Kill(t)

	close(release["first"])
	_, err = clients[host].WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)

	// New work goes to the remaining replica only.
	id, err = clients[host].ScheduleNewWorkflow(ctx, "single", api.WithInput("second"), api.WithStartTime(time.Now()))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, count(host, "second"), 1)
	}, time.Second*20, time.Millisecond*10)
	assert.Equal(t, 0, count(idle, "second"), "the removed replica must not receive pull deliveries")

	close(release["second"])
	_, err = clients[host].WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
