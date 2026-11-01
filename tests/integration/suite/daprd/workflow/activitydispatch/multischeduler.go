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

	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/cluster"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(multischeduler))
}

// multischeduler asserts that slots stay exact with three scheduler replicas.
// Every replica holds a stream to every scheduler and each scheduler
// dispatches on its own, so slots declared once per stream would let each
// scheduler fill the same slot. With slots reported by the sidecar, nine
// activities on three one-slot replicas run three at a time and the other six
// wait in the schedulers, where the backlog gauge counts them.
type multischeduler struct {
	schedulers *cluster.Cluster
	workflow   *workflow.Workflow
}

const multischedulerAppID = "pull-multischeduler"

func (m *multischeduler) Setup(t *testing.T) []framework.Option {
	configManifest := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: pullmultischeduler
spec:
  workflow:
    maxConcurrentActivityInvocations: 1
    activityDispatchMode: pull
`
	m.schedulers = cluster.New(t, cluster.WithCount(3))
	daprdOpts := func(i int) workflow.Option {
		return workflow.WithDaprdOptions(i, daprd.WithConfigManifests(t, configManifest), daprd.WithAppID(multischedulerAppID))
	}
	m.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(m.schedulers.SchedulerN(t, 0)),
		workflow.WithSchedulerAddress(m.schedulers.Addresses()[0]),
		workflow.WithSigning(false),
		workflow.WithDaprds(3),
		daprdOpts(0), daprdOpts(1), daprdOpts(2),
	)

	return []framework.Option{
		framework.WithProcesses(m.schedulers, m.workflow),
	}
}

func (m *multischeduler) Run(t *testing.T, ctx context.Context) {
	m.schedulers.WaitUntilRunning(t, ctx)
	m.workflow.WaitUntilRunning(t, ctx)

	var started [3]atomic.Int64
	releaseCh := make(chan struct{})
	for i := range 3 {
		m.workflow.RegistryN(i).AddWorkflowN("fanout", func(ctx *task.WorkflowContext) (any, error) {
			tasks := make([]task.Task, 9)
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
		m.workflow.RegistryN(i).AddActivityN("slow", func(ctx task.ActivityContext) (any, error) {
			started[i].Add(1)
			<-releaseCh
			return nil, nil
		})
	}

	client := m.workflow.BackendClientN(t, ctx, 0)
	m.workflow.BackendClientN(t, ctx, 1)
	m.workflow.BackendClientN(t, ctx, 2)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range 3 {
			resp, err := m.workflow.DaprN(i).GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1pb.GetMetadataRequest))
			if assert.NoError(c, err) {
				assert.ElementsMatch(c, m.schedulers.Addresses(), resp.GetScheduler().GetConnectedAddresses())
			}
		}
	}, time.Second*20, time.Millisecond*10)

	gauge := func(name string) float64 {
		var sum float64
		for i := range 3 {
			sum += m.schedulers.SchedulerN(t, i).Metrics(t, ctx).SumWithLabels(
				"dapr_scheduler_workflow_activity_"+name, "app_id:"+multischedulerAppID)
		}
		return sum
	}
	backlog := func() float64 { return gauge("backlog") }
	total := func() int64 {
		return started[0].Load() + started[1].Load() + started[2].Load()
	}

	id, err := client.ScheduleNewWorkflow(ctx, "fanout", api.WithStartTime(time.Now()))
	require.NoError(t, err)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		for i := range 3 {
			assert.Equal(c, int64(1), started[i].Load(), "replica %d", i)
		}
		assert.InDelta(c, 6.0, backlog(), 0, "the six activities without a slot wait in the schedulers")
		assert.InDelta(c, 3.0, gauge("inflight"), 0, "the three running activities are counted once each")
	}, time.Second*30, time.Millisecond*50)

	time.Sleep(time.Second)
	assert.Equal(t, int64(3), total())
	assert.InDelta(t, 6.0, backlog(), 0)

	// One activity finishes: its replica takes exactly one waiting activity.
	releaseCh <- struct{}{}
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(4), total())
		assert.InDelta(c, 5.0, backlog(), 0)
	}, time.Second*30, time.Millisecond*50)

	close(releaseCh)
	_, err = client.WaitForWorkflowCompletion(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, int64(9), total())
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.InDelta(c, 0.0, backlog(), 0)
	}, time.Second*20, time.Millisecond*50)
}
