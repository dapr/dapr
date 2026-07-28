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

package loadbalance

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/grpc"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(routes))
}

// routes asserts that under WorkflowsClusteredDeployment the pending-task
// waiters rendezvous with completions through the process-local pending map
// (wait_local), not the watch-stream fallback (wait_watch): the executor
// rendezvous actor shares its ID with the workflow/activity actor, so
// placement co-locates it with the waiter. A worker is connected to BOTH
// daprds so every workflow actor type's placement ring spans two hosts and
// actors spread across them: co-location is then a real property of the
// shared ID, not a single-host tautology. If the executor actor ID did not
// match the waiter's actor ID, placement would resolve it to the wrong host
// for about half the waits and they would surface as wait_watch fallbacks.
type routes struct {
	workflow *workflow.Workflow
}

func (r *routes) Setup(t *testing.T) []framework.Option {
	r.workflow = newClusteredDeployment(t, 2)

	return []framework.Option{
		framework.WithProcesses(r.workflow),
	}
}

func (r *routes) Run(t *testing.T, ctx context.Context) {
	r.workflow.WaitUntilRunning(t, ctx)

	for i := range 2 {
		require.NoError(t, r.workflow.RegistryN(i).AddWorkflowN("routes", func(ctx *task.WorkflowContext) (any, error) {
			if err := ctx.CallActivity("abc").Await(nil); err != nil {
				return nil, err
			}
			return nil, nil
		}))
		require.NoError(t, r.workflow.RegistryN(i).AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
			return nil, nil
		}))
		_ = r.workflow.BackendClientN(t, ctx, i)
	}

	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		for i := range 2 {
			assert.GreaterOrEqual(col,
				len(r.workflow.DaprN(i).GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
		}
	}, time.Second*10, time.Millisecond*10)

	client := client.NewTaskHubGrpcClient(grpc.LoadBalance(t,
		r.workflow.DaprN(0).GRPCConn(t, ctx),
		r.workflow.DaprN(1).GRPCConn(t, ctx),
	), logger.New(t))

	const n = 10
	for range n {
		id, err := client.ScheduleNewWorkflow(ctx, "routes")
		require.NoError(t, err)
		_, err = client.WaitForWorkflowCompletion(ctx, id)
		require.NoError(t, err)
	}

	// Metric recording is asynchronous; retry until the pipeline has
	// caught up with the last completion.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		routeCounts := make(map[string]float64)
		taskTypeCounts := make(map[string]float64)
		for i := range 2 {
			for k, v := range r.workflow.DaprN(i).Metrics(col, ctx).All() {
				if !strings.HasPrefix(k, "dapr_runtime_workflow_completion_route_count|") {
					continue
				}
				for label := range strings.SplitSeq(k, "|") {
					if route, ok := strings.CutPrefix(label, "route:"); ok {
						routeCounts[route] += v
					}
					if taskType, ok := strings.CutPrefix(label, "task_type:"); ok {
						taskTypeCounts[taskType] += v
					}
				}
			}
		}

		assert.Positive(col, routeCounts["wait_local"], "expected waiters to use the co-located pending map")
		assert.Zero(col, routeCounts["wait_watch"], "expected no watch-stream fallbacks in steady state")
		// The worker is pinned to daprd 0, so its completions arrive on the
		// waiter's own daprd and must be delivered without any actor
		// machinery.
		assert.Positive(col, routeCounts["complete_local"], "expected direct local-map deliveries")
		// Every wait must be matched by a completion delivered either
		// directly on this daprd or forwarded via the co-located executor
		// actor.
		assert.InDelta(col,
			routeCounts["wait_local"],
			routeCounts["complete_local"]+routeCounts["complete_actor"],
			0)
		// Both task types must be measured: one activity and at least two
		// orchestrator turns per workflow.
		assert.Positive(col, taskTypeCounts["activity"], "expected activity completions to be measured")
		assert.Positive(col, taskTypeCounts["workflow"], "expected workflow-task completions to be measured")
	}, time.Second*20, time.Millisecond*10)
}
