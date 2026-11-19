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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler/proxy"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(scaledown))
}

// scaledown checks removing a replica that neither holds nor runs a workflow
// workflow turn does not stall it or later workflow turns, which go only to the remaining replicas.
type scaledown struct {
	sched    *scheduler.Scheduler
	proxies  [3]*proxy.Proxy
	workflow *workflow.Workflow

	// delivered counts the timer turn deliveries of each workflow through
	// each daprd's own proxy.
	delivered map[string]*[3]atomic.Int64
}

const scaledownAppID = "workflow-scaledown"

func (s *scaledown) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowscaledown
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`
	s.delivered = map[string]*[3]atomic.Int64{
		"first":  new([3]atomic.Int64),
		"second": new([3]atomic.Int64),
	}
	s.sched = scheduler.New(t)
	opts := []workflow.Option{
		workflow.WithSchedulerInstance(s.sched),
		workflow.WithSigning(false),
		workflow.WithDaprds(3),
	}
	// A proxy per daprd shows which sidecar holds a workflow turn. The removed replica
	// must not hold it either: its death would cancel the workflow turn on the owner,
	// which then waits the scheduler's reattach grace.
	for i := range 3 {
		s.proxies[i] = proxy.New(t, s.sched, proxy.WithWatchJobsResponseFn(func(resp *schedulerv1pb.WatchJobsResponse) {
			if counts, ok := s.delivered[resp.GetMetadata().GetTarget().GetActor().GetId()]; ok && strings.Contains(resp.GetName(), "timer-") {
				counts[i].Add(1)
			}
		}))
		opts = append(opts, workflow.WithDaprdOptions(i, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(scaledownAppID),
			daprd.WithSchedulerAddressesReset(s.proxies[i].Address())))
	}
	s.workflow = workflow.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(s.sched, s.proxies[0], s.proxies[1], s.proxies[2], s.workflow),
	}
}

func (s *scaledown) Run(t *testing.T, ctx context.Context) {
	s.workflow.WaitUntilRunning(t, ctx)

	// held[i] counts the held workflow turns run by replica i's worker.
	var held [3]atomic.Int64
	release := map[api.InstanceID]chan struct{}{
		"first":  make(chan struct{}),
		"second": make(chan struct{}),
	}
	for i := range 3 {
		require.NoError(t, s.workflow.RegistryN(i).AddWorkflowN("hold", func(wctx *task.WorkflowContext) (any, error) {
			// The start returns once the first workflow turn commits, so hold blocks in its second.
			if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !wctx.IsReplaying {
				held[i].Add(1)
				select {
				case <-release[wctx.ID]:
				case <-ctx.Done():
				}
			}
			return nil, nil
		}))
	}

	clients := make([]*client.TaskHubGrpcClient, 3)
	for i := range 3 {
		clients[i] = s.workflow.BackendClientN(t, ctx, i)
	}

	_, err := clients[0].ScheduleNewWorkflow(ctx, "hold",
		api.WithInstanceID("first"))
	require.NoError(t, err)
	first := s.delivered["first"]
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), held[0].Load()+held[1].Load()+held[2].Load())
		assert.Equal(c, int64(1), first[0].Load()+first[1].Load()+first[2].Load())
	}, time.Second*10, time.Millisecond*10)
	removed := -1
	for i := range 3 {
		if held[i].Load() == 0 && first[i].Load() == 0 {
			removed = i
			break
		}
	}
	remaining := []int{(removed + 1) % 3, (removed + 2) % 3}

	// Wait for the scheduler to drop the removed replica's streams.
	streams := int(s.workflow.Scheduler().Metrics(t, ctx).All()["dapr_scheduler_sidecars_connected"])
	s.workflow.DaprN(removed).Kill(t)
	s.workflow.Scheduler().WaitUntilSidecarsConnected(t, ctx, streams-streams/3)

	close(release["first"])
	meta, err := clients[remaining[0]].WaitForWorkflowCompletion(ctx, "first")
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))

	// New workflow turns go to the remaining replicas only.
	_, err = clients[remaining[0]].ScheduleNewWorkflow(ctx, "hold",
		api.WithInstanceID("second"))
	require.NoError(t, err)
	second := s.delivered["second"]
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(2), held[remaining[0]].Load()+held[remaining[1]].Load())
		assert.Equal(c, int64(1), second[remaining[0]].Load()+second[remaining[1]].Load())
	}, time.Second*10, time.Millisecond*10)
	assert.Equal(t, int64(0), held[removed].Load()+second[removed].Load(), "the removed replica must not get workflow turns")
	s.workflow.Scheduler().WaitUntilPullInflight(t, ctx, 1, "app_id:"+scaledownAppID)

	close(release["second"])
	meta, err = clients[remaining[0]].WaitForWorkflowCompletion(ctx, "second")
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
}
