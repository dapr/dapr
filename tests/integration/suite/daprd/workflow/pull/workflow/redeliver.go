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
	suite.Register(new(redeliver))
}

// redeliver checks that when the sidecar holding a pull workflow turn dies,
// the scheduler delivers the workflow turn to the surviving sidecar and the workflow completes.
type redeliver struct {
	sched    *scheduler.Scheduler
	proxies  [2]*proxy.Proxy
	workflow *workflow.Workflow

	// delivered counts the deliveries of hold's timer turn through each
	// daprd's own proxy.
	delivered [2]atomic.Int64
}

const redeliverAppID = "workflow-redeliver"

func (r *redeliver) Setup(t *testing.T) []framework.Option {
	cfg := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: workflowredeliver
spec:
  workflow:
    maxConcurrentWorkflowInvocations: 1
    maxConcurrentActivityInvocations: 1
    dispatchMode: pull
`
	r.sched = scheduler.New(t)
	for i := range 2 {
		r.proxies[i] = proxy.New(t, r.sched, proxy.WithWatchJobsResponseFn(func(resp *schedulerv1pb.WatchJobsResponse) {
			if resp.GetMetadata().GetTarget().GetActor().GetId() == "hold" && strings.Contains(resp.GetName(), "timer-") {
				r.delivered[i].Add(1)
			}
		}))
	}
	// A proxy per daprd shows which sidecar a workflow turn is delivered to.
	r.workflow = workflow.New(t,
		workflow.WithSchedulerInstance(r.sched),
		workflow.WithSigning(false),
		workflow.WithDaprds(2),
		workflow.WithDaprdOptions(0, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(redeliverAppID),
			daprd.WithSchedulerAddressesReset(r.proxies[0].Address())),
		workflow.WithDaprdOptions(1, daprd.WithConfigManifests(t, cfg), daprd.WithAppID(redeliverAppID),
			daprd.WithSchedulerAddressesReset(r.proxies[1].Address())),
	)

	return []framework.Option{
		framework.WithProcesses(r.sched, r.proxies[0], r.proxies[1], r.workflow),
	}
}

func (r *redeliver) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	var held atomic.Int64
	release := make(chan struct{})
	for i := range 2 {
		require.NoError(t, r.workflow.RegistryN(i).AddWorkflowN("hold", func(wctx *task.WorkflowContext) (any, error) {
			// The start returns once the first workflow turn commits, so hold blocks in its second.
			if err := wctx.CreateTimer(time.Millisecond).Await(nil); err != nil {
				return nil, err
			}
			if !wctx.IsReplaying {
				held.Add(1)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			return nil, nil
		}))
	}

	// A client per daprd so the wait below can go through the survivor.
	clients := []*client.TaskHubGrpcClient{
		r.workflow.BackendClientN(t, ctx, 0),
		r.workflow.BackendClientN(t, ctx, 1),
	}

	_, err := clients[0].ScheduleNewWorkflow(ctx, "hold",
		api.WithInstanceID("hold"))
	require.NoError(t, err)
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), held.Load())
		assert.Equal(c, int64(1), r.delivered[0].Load()+r.delivered[1].Load())
	}, time.Second*10, time.Millisecond*10)
	r.workflow.Scheduler().WaitUntilPullInflight(t, ctx, 1, "app_id:"+redeliverAppID)

	victim := 0
	if r.delivered[1].Load() == 1 {
		victim = 1
	}
	survivor := 1 - victim
	r.workflow.DaprN(victim).Kill(t)

	// Longer: the workflow turn is delivered again only after the scheduler's reattach
	// grace for the dead sidecar.
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.Equal(c, int64(1), r.delivered[survivor].Load(), "the scheduler must deliver the lost turn to the surviving sidecar")
	}, time.Second*60, time.Millisecond*10)
	r.workflow.Scheduler().WaitUntilPullInflight(t, ctx, 1, "app_id:"+redeliverAppID)

	close(release)
	meta, err := clients[survivor].WaitForWorkflowCompletion(ctx, "hold")
	require.NoError(t, err)
	assert.True(t, api.WorkflowMetadataIsComplete(meta))
	assert.Equal(t, int64(1), r.delivered[victim].Load())
}
