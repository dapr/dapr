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
	"strings"
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

// backlog asserts that pull activities beyond the fleet's slots wait in the
// scheduler, that the wait is visible on the scheduler's backlog gauge, and
// that the backlog drains as slots free up.
type backlog struct {
	workflow *workflow.Workflow
}

const backlogAppID = "pull-backlog"

func (b *backlog) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullbacklog
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	b.workflow = workflow.New(t,
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(backlogAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(backlogAppID)),
	)

	return []framework.Option{
		framework.WithProcesses(b.workflow),
	}
}

func (b *backlog) Run(t *testing.T, ctx context.Context) {
	b.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	releaseCh := make(chan struct{})

	for i := range 2 {
		b.workflow.RegistryN(i).AddWorkflowN("pair", func(ctx *task.WorkflowContext) (any, error) {
			t1 := ctx.CallActivity("slow")
			t2 := ctx.CallActivity("slow")
			if err := t1.Await(nil); err != nil {
				return nil, err
			}
			return nil, t2.Await(nil)
		})
		b.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			inside.Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	client := b.workflow.BackendClientN(t, ctx, 0)
	b.workflow.BackendClientN(t, ctx, 1)
	ids := make([]api.InstanceID, 0, 2)
	for range 2 {
		id, err := client.ScheduleNewWorkflow(ctx, "pair", api.WithStartTime(time.Now()))
		require.NoError(t, err)
		ids = append(ids, id)
	}

	backlogGauge := func(c *assert.CollectT) float64 {
		m := b.workflow.Scheduler().Metrics(t, ctx)
		v := m.SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog",
			"app_id:"+backlogAppID,
			"activity_name:slow",
		)
		if c != nil {
			for k, val := range m.All() {
				if strings.Contains(k, "backlog") {
					t.Logf("%s=%v", k, val)
				}
			}
		}
		return v
	}

	// Two slots in the fleet, four activities scheduled: two run, two wait.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
		assert.InDelta(c, 2.0, backlogGauge(c), 0)
	}, time.Second*20, time.Millisecond*10)

	time.Sleep(time.Second)
	assert.Equal(t, int64(2), inside.Load())

	// Each release frees one slot for one waiting activity.
	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(3), inside.Load())
		assert.InDelta(c, 1.0, backlogGauge(c), 0)
	}, time.Second*20, time.Millisecond*10)

	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(4), inside.Load())
		assert.InDelta(c, 0.0, backlogGauge(c), 0)
	}, time.Second*20, time.Millisecond*10)

	close(releaseCh)
	for _, id := range ids {
		_, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
	}
}
