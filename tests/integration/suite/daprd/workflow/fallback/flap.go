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
	"sync/atomic"
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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(flap))
}

// flap connects and disconnects a worker on one daprd of an app over and
// over, each time for less than the damping period, as a crash looping
// worker does. The app's other daprd, which has no worker, stays a fallback
// host throughout rather than giving the workflow actor types up and taking
// them back on each connect and leave. Once the worker stays connected for
// the damping period, it gives them up.
type flap struct {
	workflow *workflow.Workflow
}

const flapDamping = 4 * time.Second

func (f *flap) Setup(t *testing.T) []framework.Option {
	appID := uuid.NewString()
	f.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithFallbackHosts(),
		workflow.WithDaprdOptions(0, daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithAppID(appID), withDamping(t, flapDamping)),
	)

	return []framework.Option{
		framework.WithProcesses(f.workflow),
	}
}

func (f *flap) Run(t *testing.T, ctx context.Context) {
	f.workflow.WaitUntilRunning(t, ctx)

	require.NoError(t, f.workflow.RegistryN(0).AddWorkflowN("wf", func(ctx *task.WorkflowContext) (any, error) {
		return "done", nil
	}))

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.True(c, hostsWorkflows(c, ctx, f.workflow, 1))
	}, time.Second*10, time.Millisecond*50)

	// Watch the daprd without a worker throughout the flapping.
	var unhosted atomic.Bool
	wctx, wcancel := context.WithCancel(ctx)
	watched := make(chan struct{})
	go func() {
		defer close(watched)
		for wctx.Err() == nil {
			var rerr readErrors
			if !hostsWorkflows(&rerr, wctx, f.workflow, 1) && !rerr.failed {
				unhosted.Store(true)
			}
			time.Sleep(time.Millisecond * 50)
		}
	}()

	mgmt := f.workflow.ManagementClientN(t, ctx, 1)
	var ids []api.InstanceID
	for range 5 {
		worker := f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
		f.workflow.WaitForConnectedWorkersN(t, ctx, 0, 1)

		// Calls through the fallback host keep working.
		sctx, cancel := context.WithTimeout(ctx, time.Second*10)
		id, err := mgmt.ScheduleNewWorkflow(sctx, "wf", api.WithStartTime(time.Now()))
		cancel()
		require.NoError(t, err)
		ids = append(ids, id)

		time.Sleep(flapDamping / 4)
		worker.Disconnect(t)
		f.workflow.WaitForNoConnectedWorkersN(t, ctx, 0)
		time.Sleep(flapDamping / 8)
	}

	wcancel()
	<-watched
	assert.False(t, unhosted.Load(), "the daprd without a worker gave the workflow actor types up while the worker flapped")

	// A worker which stays: the fallback host gives the types up, but not
	// before the damping period.
	connected := time.Now()
	f.workflow.ConnectWorkerN(t, ctx, 0, f.workflow.RegistryN(0))
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.False(c, hostsWorkflows(c, ctx, f.workflow, 1))
	}, time.Second*20, time.Millisecond*50)
	assert.GreaterOrEqual(t, time.Since(connected), flapDamping-time.Second)

	for _, id := range ids {
		meta, err := mgmt.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err, id)
		assert.Equal(t, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED, meta.GetRuntimeStatus(), id)
	}
}
