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

// multischeduler checks workflow slots stay exact with three schedulers: nine
// workflows on three one-slot replicas hold three slots and the rest wait.
type multischeduler struct {
	schedulers *cluster.Cluster
	workflow   *workflow.Workflow
}

const multischedulerAppID = "workflow-multischeduler"

func (m *multischeduler) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowmultischeduler
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`
	m.schedulers = cluster.New(t, cluster.WithCount(3))
	daprdOpts := func(i int) workflow.Option {
		return workflow.WithDaprdOptions(i, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(multischedulerAppID))
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

	var (
		seen    sync.Map
		running atomic.Int64
		peak    atomic.Int64
	)
	release := make(chan struct{})
	done := ctx.Done()
	for i := range 3 {
		require.NoError(t, m.workflow.RegistryN(i).AddWorkflowN("hold", func(ctx *task.WorkflowContext) (any, error) {
			// The start returns once the first workflow turn commits, so hold blocks in its second.
			if err := ctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !ctx.IsReplaying {
				// A redelivered workflow turn runs this again, so each instance blocks once.
				if _, loaded := seen.LoadOrStore(ctx.ID, true); !loaded {
					n := running.Add(1)
					for {
						p := peak.Load()
						if n <= p || peak.CompareAndSwap(p, n) {
							break
						}
					}
					select {
					case <-release:
					case <-done:
					}
					running.Add(-1)
				}
			}
			return nil, nil
		}))
	}

	client := m.workflow.BackendClientN(t, ctx, 0)
	m.workflow.BackendClientN(t, ctx, 1)
	m.workflow.BackendClientN(t, ctx, 2)

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		for i := range 3 {
			resp, err := m.workflow.DaprN(i).GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1pb.GetMetadataRequest))
			if assert.NoError(col, err) {
				assert.ElementsMatch(col, m.schedulers.Addresses(), resp.GetScheduler().GetConnectedAddresses())
			}
		}
	}, time.Second*10, time.Millisecond*10)

	gauge := func(col assert.TestingT, name string) float64 {
		var sum float64
		for i := range 3 {
			sum += m.schedulers.SchedulerN(t, i).Metrics(col, ctx).SumWithLabels(
				"dapr_scheduler_workflow_"+name, "app_id:"+multischedulerAppID)
		}
		return sum
	}

	// A start blocks until its first workflow turn runs, so the starts run in the background.
	ids := make([]api.InstanceID, 9)
	errCh := make(chan error, len(ids))
	for i := range ids {
		ids[i] = api.InstanceID(fmt.Sprintf("hold-%d", i))
		go func() {
			_, err := client.ScheduleNewWorkflow(ctx, "hold",
				api.WithInstanceID(ids[i]))
			errCh <- err
		}()
	}

	// Each workflow has one workflow turn waiting: three hold a slot and six wait in
	// the schedulers.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.InDelta(col, 3.0, gauge(col, "inflight"), 0)
		assert.InDelta(col, 6.0, gauge(col, "backlog"), 0)
		assert.Positive(col, running.Load())
	}, time.Second*10, time.Millisecond*10)
	assert.LessOrEqual(t, peak.Load(), int64(3))

	// One workflow finishes and frees its slot for exactly one waiting workflow turn.
	select {
	case release <- struct{}{}:
	case <-time.After(time.Second * 10):
		require.Fail(t, "no held workflow turn to release")
	}
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.InDelta(col, 3.0, gauge(col, "inflight"), 0)
		assert.InDelta(col, 5.0, gauge(col, "backlog"), 0)
	}, time.Second*10, time.Millisecond*10)
	assert.LessOrEqual(t, peak.Load(), int64(3))

	close(release)
	for range ids {
		select {
		case err := <-errCh:
			require.NoError(t, err)
		case <-time.After(time.Second * 10):
			require.Fail(t, "workflow did not start")
		}
	}
	for _, id := range ids {
		meta, err := client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
		assert.True(t, api.WorkflowMetadataIsComplete(meta), string(id))
	}
	assert.LessOrEqual(t, peak.Load(), int64(3))
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		assert.InDelta(col, 0.0, gauge(col, "backlog"), 0)
	}, time.Second*10, time.Millisecond*10)
}
