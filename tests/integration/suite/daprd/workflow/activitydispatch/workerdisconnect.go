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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workerdisconnect))
}

// workerdisconnect asserts that a replica whose application worker has gone
// away stops being a pull target: with its activity actor type unregistered
// it offers no slots, so all work goes to the replica that still has a
// worker instead of being delivered to a sidecar that cannot run it.
type workerdisconnect struct {
	workflow *workflow.Workflow
}

func (w *workerdisconnect) Setup(t *testing.T) []framework.Option {
	const appID = "pull-workerdisconnect"
	cfg := pullConfig("pullworkerdisconnect", 1)
	w.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
	)
	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workerdisconnect) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	var started [2]atomic.Int64
	releaseCh := make(chan struct{})
	for i := range 2 {
		w.workflow.RegistryN(i).AddWorkflowN("pair", func(ctx *task.WorkflowContext) (any, error) {
			t1 := ctx.CallActivity("slow")
			t2 := ctx.CallActivity("slow")
			if err := t1.Await(nil); err != nil {
				return nil, err
			}
			return nil, t2.Await(nil)
		})
		w.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	client0 := w.workflow.BackendClientN(t, ctx, 0)
	workerCtx, stopWorker := context.WithCancel(ctx)
	w.workflow.BackendClientN(t, workerCtx, 1)

	// Replica 1 loses its worker: its workflow actor types unregister and its
	// scheduler stream reconnects without them.
	stopWorker()
	w.workflow.WaitForNoConnectedWorkersN(t, ctx, 1)

	id, err := client0.ScheduleNewWorkflow(ctx, "pair", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), started[0].Load())
	}, time.Second*20, time.Millisecond*10)
	time.Sleep(time.Second)
	assert.Equal(t, int64(0), started[1].Load(), "a replica without a worker must not receive pull deliveries")

	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), started[0].Load(), "the second activity waits for replica 0's slot rather than stalling")
	}, time.Second*20, time.Millisecond*10)
	assert.Equal(t, int64(0), started[1].Load())

	close(releaseCh)
	_, err = client0.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
