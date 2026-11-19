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

package workflow

import (
	"context"
	"sync"
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
	suite.Register(new(order))
}

// order checks waiting workflow turns are served by priority.
type order struct {
	workflow *workflow.Workflow
}

const orderAppID = "workflow-order"

func (o *order) Setup(t *testing.T) []framework.Option {
	o.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithAppID(orderAppID), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workfloworder
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
    priority:
      workflows:
        - name: reports
          priorityLevel: low
        - name: payments
          priorityLevel: critical
`)),
	)

	return []framework.Option{
		framework.WithProcesses(o.workflow),
	}
}

func (o *order) Run(t *testing.T, ctx context.Context) {
	o.workflow.WaitUntilRunning(t, ctx)

	var (
		lock sync.Mutex
		ran  []string
	)
	held := make(chan struct{})
	var heldOnce sync.Once
	release := make(chan struct{})
	registry := o.workflow.Registry()
	require.NoError(t, registry.AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		// The start returns once the first workflow turn commits, so hold blocks in its second.
		if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			// A redelivered workflow turn runs this again.
			heldOnce.Do(func() { close(held) })
			<-release
		}
		return nil, nil
	}))
	for _, name := range []string{"reports", "payments"} {
		require.NoError(t, registry.AddWorkflowN(name, func(ctx *task.WorkflowContext) (any, error) {
			if !ctx.IsReplaying {
				lock.Lock()
				ran = append(ran, name)
				lock.Unlock()
			}
			return nil, nil
		}))
	}

	client := o.workflow.BackendClient(t, ctx)
	hold, err := client.ScheduleNewWorkflow(ctx, "hold")
	require.NoError(t, err)
	<-held

	// These first workflow turns wait in the scheduler while hold has the slot.
	backlog := func(want float64) {
		t.Helper()
		assert.EventuallyWithT(t, func(col *assert.CollectT) {
			assert.InDelta(col, want, o.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
				"dapr_scheduler_workflow_activity_backlog", "app_id:"+orderAppID), 0)
		}, time.Second*10, time.Millisecond*10)
	}
	errCh := make(chan error, 2)
	for i, name := range []string{"reports", "payments"} {
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, name,
				api.WithInstanceID(api.InstanceID(name)))
			errCh <- err
		}()
		backlog(float64(i + 1))
	}
	lock.Lock()
	assert.Empty(t, ran)
	lock.Unlock()

	close(release)
	for range 2 {
		require.NoError(t, <-errCh)
	}
	for _, id := range []api.InstanceID{hold, "reports", "payments"} {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta))
	}

	lock.Lock()
	defer lock.Unlock()
	// payments is critical and reports is low, so payments runs first though it started second.
	assert.Equal(t, []string{"payments", "reports"}, ran)
}
