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
	suite.Register(new(hashed))
}

// hashed asserts that the explicit default mode leaves today's behaviour in
// place even with a per-sidecar cap configured: no slots are offered to the
// scheduler, no backlog gauge appears, and activities still complete.
type hashed struct {
	workflow *workflow.Workflow
}

func (h *hashed) Setup(t *testing.T) []framework.Option {
	const appID = "pull-hashed"
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: hasheddispatch
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: hashed
`
	h.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(appID)),
	)
	return []framework.Option{
		framework.WithProcesses(h.workflow),
	}
}

func (h *hashed) Run(t *testing.T, ctx context.Context) {
	h.workflow.WaitUntilRunning(t, ctx)

	var started atomic.Int64
	releaseCh := make(chan struct{})
	for i := range 2 {
		h.workflow.RegistryN(i).AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, 4)
			for j := range tasks {
				tasks[j] = ctx.CallActivity("slow")
			}
			for _, tk := range tasks {
				if err := tk.Await(nil); err != nil {
					return nil, err
				}
			}
			return nil, nil
		})
		h.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started.Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	client := h.workflow.BackendClientN(t, ctx, 0)
	h.workflow.BackendClientN(t, ctx, 1)

	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c, started.Load(), int64(1))
	}, time.Second*20, time.Millisecond*10)

	// Hashed dispatch never parks work in the scheduler.
	for key := range h.workflow.Scheduler().Metrics(t, ctx).All() {
		assert.NotContains(t, key, "workflow_activity_backlog", "unexpected pull backlog metric")
	}

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(4), started.Load())
}
