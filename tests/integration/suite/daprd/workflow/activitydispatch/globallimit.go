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
	suite.Register(new(globallimit))
}

// globallimit asserts that globalMaxConcurrentActivityInvocations still caps
// the fleet under pull dispatch even when the replicas offer more slots than
// the cap, and that activities held back by the global gate count towards the
// backlog gauge.
type globallimit struct {
	workflow *workflow.Workflow
}

const globallimitAppID = "pull-globallimit"

func (g *globallimit) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullgloballimit
spec:
  workflow:
    maxConcurrentActivityInvocations: 2
    globalMaxConcurrentActivityInvocations: 2
    activityDispatchMode: pull
`
	g.workflow = workflow.New(t,
		workflow.WithDaprds(3),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(globallimitAppID)),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(globallimitAppID)),
		workflow.WithDaprdOptions(2, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(globallimitAppID)),
	)
	return []framework.Option{
		framework.WithProcesses(g.workflow),
	}
}

func (g *globallimit) Run(t *testing.T, ctx context.Context) {
	g.workflow.WaitUntilRunning(t, ctx)

	var inside atomic.Int64
	releaseCh := make(chan struct{})
	for i := range 3 {
		g.workflow.RegistryN(i).AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, 6)
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
		g.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			inside.Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	client := g.workflow.BackendClientN(t, ctx, 0)
	g.workflow.BackendClientN(t, ctx, 1)
	g.workflow.BackendClientN(t, ctx, 2)

	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	backlog := func() float64 {
		return g.workflow.Scheduler().Metrics(t, ctx).SumWithLabels(
			"dapr_scheduler_workflow_activity_backlog", "app_id:"+globallimitAppID)
	}

	// Six slots across the fleet, but the global cap admits two.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), inside.Load())
		assert.InDelta(c, 4.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*10)
	time.Sleep(time.Second)
	assert.Equal(t, int64(2), inside.Load())

	for want := int64(3); want <= 6; want++ {
		releaseCh <- struct{}{}
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.Equal(c, want, inside.Load())
		}, time.Second*20, time.Millisecond*10)
	}

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
}
