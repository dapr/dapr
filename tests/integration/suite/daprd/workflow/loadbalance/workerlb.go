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
	"github.com/dapr/durabletask-go/api"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

func init() {
	suite.Register(new(workerlb))
}

// workerlb runs the WORKER itself behind a per-call round-robin load
// balancer, mirroring an L7 proxy in front of a clustered deployment: the
// GetWorkItems stream lands on one daprd while every unary
// CompleteActivityTask/CompleteWorkflowTask call independently round-robins
// across both. Completions landing on the daprd that does not host the
// pending work item must be forwarded to the waiter through the co-located
// executor rendezvous actor.
type workerlb struct {
	workflow *workflow.Workflow
}

func (w *workerlb) Setup(t *testing.T) []framework.Option {
	w.workflow = workflow.NewClustered(t, 2)

	return []framework.Option{
		framework.WithProcesses(w.workflow),
	}
}

func (w *workerlb) Run(t *testing.T, ctx context.Context) {
	w.workflow.WaitUntilRunning(t, ctx)

	registry := task.NewTaskRegistry()
	require.NoError(t, registry.AddWorkflowN("workerlb", func(ctx *task.WorkflowContext) (any, error) {
		var out string
		if err := ctx.CallActivity("abc", task.WithActivityInput("abc")).Await(&out); err != nil {
			return nil, err
		}
		return out, nil
	}))
	require.NoError(t, registry.AddActivityN("abc", func(ctx task.ActivityContext) (any, error) {
		return "done", nil
	}))

	lbconn := grpc.LoadBalance(t,
		w.workflow.DaprN(0).GRPCConn(t, ctx),
		w.workflow.DaprN(1).GRPCConn(t, ctx),
	)
	client := client.NewTaskHubGrpcClient(lbconn, logger.New(t))
	require.NoError(t, client.StartWorkItemListener(ctx, registry))

	// The single work-item stream landed on one of the two daprds; wait for
	// it to register the workflow actor types.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		total := 0
		for i := range 2 {
			total += len(w.workflow.DaprN(i).GetMetadata(t, ctx).ActorRuntime.ActiveActors)
		}
		assert.GreaterOrEqual(col, total, 3)
	}, time.Second*10, time.Millisecond*10)

	const n = 10
	ids := make([]api.InstanceID, n)

	var err error
	for i := range n {
		ids[i], err = client.ScheduleNewWorkflow(ctx, "workerlb")
		require.NoError(t, err)
	}

	for i := range n {
		metadata, werr := client.WaitForWorkflowCompletion(ctx, ids[i])
		require.NoError(t, werr)
		assert.Equal(t, `"done"`, metadata.GetOutput().GetValue())
	}

	// Metric recording is asynchronous; retry until the pipeline has
	// caught up with the last completion.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		routeCounts := make(map[string]float64)
		for i := range 2 {
			for k, v := range w.workflow.DaprN(i).Metrics(col, ctx).All() {
				if !strings.HasPrefix(k, "dapr_runtime_workflow_completion_route_count|") {
					continue
				}
				for label := range strings.SplitSeq(k, "|") {
					if route, ok := strings.CutPrefix(label, "route:"); ok {
						routeCounts[route] += v
					}
				}
			}
		}

		// With every completion independently round-robined across two
		// daprds, some must have landed on the non-hosting daprd and been
		// forwarded via the executor actor.
		assert.Positive(col, routeCounts["complete_actor"], "expected some completions to be forwarded via the executor actor")
		assert.Zero(col, routeCounts["wait_watch"], "expected no watch-stream fallbacks: the rendezvous actor must be co-located")
	}, time.Second*20, time.Millisecond*10)
}
