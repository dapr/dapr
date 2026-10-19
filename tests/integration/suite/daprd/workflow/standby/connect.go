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

package standby

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(connect))
}

// connect starts workflows without pause while a worker connects, several
// times over. The workflow actor type is hosted throughout the switch from
// the standby host to the active one, so every start succeeds, including one
// still in flight on a standby actor when the switch happens.
type connect struct {
	workflow *workflow.Workflow
}

func (c *connect) Setup(t *testing.T) []framework.Option {
	c.workflow = workflow.New(t,
		workflow.WithStandbyHosts(),
	)

	return []framework.Option{
		framework.WithProcesses(c.workflow),
	}
}

func (c *connect) Run(t *testing.T, ctx context.Context) {
	c.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, c.workflow.Registry().AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return "done", nil
	}))

	mgmt := c.workflow.ManagementClient(t, ctx)

	const rounds = 10
	var ids []api.InstanceID
	for round := range rounds {
		octx, cancel := context.WithTimeout(ctx, time.Second*10)

		// Wait for the standby host to serve starts before racing the
		// connect against them.
		require.EventuallyWithT(t, func(col *assert.CollectT) {
			id, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(api.InstanceID(fmt.Sprintf("standby-connect-%d-ready", round))))
			if assert.NoError(col, err) {
				ids = append(ids, id)
			}
		}, time.Second*5, time.Millisecond*50)

		stop := make(chan struct{})
		var wg sync.WaitGroup
		var lock sync.Mutex
		for g := range 4 {
			wg.Go(func() {
				for n := 0; ; n++ {
					select {
					case <-stop:
						return
					default:
					}
					id, err := mgmt.ScheduleNewWorkflow(octx, "wf", api.WithInstanceID(api.InstanceID(fmt.Sprintf("standby-connect-%d-%d-%d", round, g, n))))
					assert.NoError(t, err, "start during the connect of round %d", round)
					lock.Lock()
					ids = append(ids, id)
					lock.Unlock()
				}
			})
		}

		worker := c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())
		time.Sleep(time.Millisecond * 200)
		close(stop)
		wg.Wait()
		cancel()

		worker.Disconnect(t)
		c.workflow.WaitForNoConnectedWorkers(t, ctx)
	}

	c.workflow.ConnectWorker(t, ctx, c.workflow.Registry())
	for _, id := range ids {
		if id == "" {
			continue
		}
		meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
	}
}
