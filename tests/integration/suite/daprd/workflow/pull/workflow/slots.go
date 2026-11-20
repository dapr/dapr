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
	suite.Register(new(slots))
}

// slots checks two workflow slots on one sidecar run two workflow turns at once, never
// more, and waiting workflow turns start as slots free. One replica, since a workflow turn runs
// on its placement owner and two replicas would make where it runs vary.
type slots struct {
	workflow *workflow.Workflow
}

const slotsAppID = "workflow-slots"

func (s *slots) Setup(t *testing.T) []framework.Option {
	s.workflow = workflow.New(t,
		workflow.WithDaprdOptions(0, daprd.WithAppID(slotsAppID), daprd.WithConfigManifests(t, `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowslots
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 2
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`)),
	)

	return []framework.Option{
		framework.WithProcesses(s.workflow),
	}
}

func (s *slots) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	var (
		lock    sync.Mutex
		entered = make(map[api.InstanceID]bool)
		running int
		peak    int
	)
	counts := func() (int, int, int) {
		lock.Lock()
		defer lock.Unlock()
		return len(entered), running, peak
	}
	release := make(chan struct{})
	require.NoError(t, s.workflow.Registry().AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
		// The start returns once the first workflow turn commits, so hold blocks in its second.
		if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
			return nil, err
		}
		if !ctx.IsReplaying {
			lock.Lock()
			entered[ctx.ID] = true
			running++
			peak = max(peak, running)
			lock.Unlock()
			<-release
			lock.Lock()
			running--
			lock.Unlock()
		}
		return nil, nil
	}))

	client := s.workflow.BackendClient(t, ctx)

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

	backlog := func() float64 {
		return s.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_backlog", "app_id:"+slotsAppID)
	}

	// Two slots and four workflows: two hold a slot, two have a workflow turn waiting.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		n, run, _ := counts()
		assert.Equal(c, 2, n)
		assert.Equal(c, 2, run)
		assert.InDelta(c, 2.0, backlog(), 0)
	}, time.Second*10, time.Millisecond*10)

	// Each release frees one slot for one waiting workflow.
	for want := 3; want <= 4; want++ {
		select {
		case release <- struct{}{}:
		case <-time.After(time.Second * 10):
			require.Fail(t, "no held workflow took the release")
		}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			n, run, _ := counts()
			assert.Equal(c, want, n)
			assert.Equal(c, 2, run)
			assert.InDelta(c, float64(4-want), backlog(), 0)
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

	_, _, p := counts()
	assert.Equal(t, 2, p, "no more than two turns run at once")
}
