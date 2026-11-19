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
	"fmt"
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
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(backlog))
}

// backlog checks workflow turns beyond the replica's workflow slots wait in
// the scheduler, show on the backlog gauge, and drain as slots free. One
// replica with two slots, since a workflow turn runs on its owner.
type backlog struct {
	workflow *workflow.Workflow
}

const backlogAppID = "workflow-backlog"

func (b *backlog) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowbacklog
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 2
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`
	b.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(backlogAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *backlog) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var (
		entered sync.Map
		inside  atomic.Int64
	)
	release := make(chan struct{})
	for i := range 1 {
		require.NoError(t, b.workflow.RegistryN(i).AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
			// The start returns once the first workflow turn commits, so hold blocks in its second.
			if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !ctx.IsReplaying {
				if _, ok := entered.LoadOrStore(ctx.ID, true); !ok {
					inside.Add(1)
				}
				<-release
			}
			return nil, nil
		}))
	}

	client := b.workflow.BackendClientN(t, ctx, 0)

	ids := make([]api.InstanceID, 4)
	errCh := make(chan error, len(ids))
	for i := range ids {
		ids[i] = api.InstanceID(fmt.Sprintf("hold-%d", i))
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, "hold",
				api.WithInstanceID(ids[i]))
			errCh <- err
		}()
	}

	backlogGauge := func() float64 {
		return b.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog",
			"app_id:"+backlogAppID,
			"activity_name:hold",
		)
	}

	// Two slots in the fleet and four workflows: two hold a slot, two have a workflow turn waiting.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
		assert.InDelta(c, 2.0, backlogGauge(), 0)
	}, time.Second*10, time.Millisecond*10)

	// Each release frees one slot for one waiting workflow.
	for want := int64(3); want <= 4; want++ {
		select {
		case release <- struct{}{}:
		case <-time.After(time.Second * 10):
			require.Fail(t, "no held workflow took the release")
		}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, want, inside.Load())
			assert.InDelta(c, float64(4-want), backlogGauge(), 0)
		}, time.Second*10, time.Millisecond*10)
	}

	close(release)
	for range ids {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-ctx.Done():
			require.Fail(t, "workflow start did not return")
		}
	}
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
}
