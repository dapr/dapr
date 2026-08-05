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

package stalled

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/workflow"
	wf "github.com/dapr/dapr/tests/integration/framework/workflow"
	"github.com/dapr/durabletask-go/api/protos"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

// PermutationOptions configures the worker client placement for a
// Permutation. Each phase's slice lists the daprd index each worker client
// connects to; duplicates mean multiple clients on the same daprd.
type PermutationOptions struct {
	Daprds   int
	V1       []int
	V2       []int
	Recovery []int
}

// Permutation is a clustered deployment process which drives the
// stall-and-recover scenario for a given placement of worker clients.
type Permutation struct {
	workflow *workflow.Workflow
	opts     PermutationOptions
}

func NewPermutation(t *testing.T, opts PermutationOptions) *Permutation {
	t.Helper()
	return &Permutation{
		workflow: workflow.NewClustered(t, opts.Daprds),
		opts:     opts,
	}
}

func (p *Permutation) Run(t *testing.T, ctx context.Context) {
	t.Helper()
	p.workflow.Run(t, ctx)
}

func (p *Permutation) Cleanup(t *testing.T) {
	t.Helper()
	p.workflow.Cleanup(t)
}

func (p *Permutation) Execute(t *testing.T, ctx context.Context) {
	t.Helper()

	p.workflow.WaitUntilRunning(t, ctx)

	var completed atomic.Bool

	observers := make([]*client.TaskHubGrpcClient, p.opts.Daprds)
	for i := range observers {
		observers[i] = client.NewTaskHubGrpcClient(p.workflow.DaprN(i).GRPCConn(t, ctx), logger.New(t))
	}

	newWorkers := func(t *testing.T, ctx context.Context, version string, idxs []int) {
		t.Helper()
		expected := make(map[int]int)
		for _, idx := range idxs {
			registry := task.NewTaskRegistry()
			require.NoError(t, registry.AddVersionedWorkflowN("workflow", version, true, func(ctx *task.WorkflowContext) (any, error) {
				if err := ctx.WaitForSingleEvent("Continue", -1).Await(nil); err != nil {
					return nil, err
				}
				completed.Store(true)
				return nil, nil
			}))
			worker := client.NewTaskHubGrpcClient(p.workflow.DaprN(idx).GRPCConn(t, ctx), logger.New(t))
			require.NoError(t, worker.StartWorkItemListener(ctx, registry))
			expected[idx]++
		}

		for idx, count := range expected {
			assert.EventuallyWithT(t, func(c *assert.CollectT) {
				md := p.workflow.DaprN(idx).GetMetadata(c, ctx)
				if !assert.NotNil(c, md) || !assert.NotNil(c, md.Workflows) {
					return
				}
				assert.GreaterOrEqual(c, md.Workflows.ConnectedWorkers, count)
			}, time.Second*30, time.Millisecond*10)
		}
	}

	disconnectWorkers := func(t *testing.T, ctx context.Context, cancel context.CancelFunc) {
		t.Helper()
		cancel()
		for i := range p.opts.Daprds {
			p.workflow.WaitForNoConnectedWorkersN(t, ctx, i)
		}
	}

	workerCtx, cancelWorkers := context.WithCancel(ctx)
	newWorkers(t, workerCtx, "v1", p.opts.V1)
	id, err := observers[0].ScheduleNewWorkflow(ctx, "workflow")
	require.NoError(t, err)
	wf.WaitForWorkflowStartedEvent(t, ctx, observers[0], id)

	disconnectWorkers(t, ctx, cancelWorkers)
	workerCtx, cancelWorkers = context.WithCancel(ctx)
	newWorkers(t, workerCtx, "v2", p.opts.V2)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.NoError(c, observers[p.opts.Daprds-1].RaiseEvent(ctx, id, "Continue"))
	}, time.Second*20, time.Millisecond*10)

	for i := range observers {
		wf.WaitForRuntimeStatus(t, ctx, observers[i], id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_STALLED)
		lastEvent := wf.GetLastHistoryEventOfType[protos.HistoryEvent_ExecutionStalled](t, ctx, observers[i], id)
		require.NotNil(t, lastEvent)
		require.Equal(t, protos.StalledReason_VERSION_NOT_AVAILABLE, lastEvent.GetExecutionStalled().GetReason())
		require.Equal(t, "Version not available: v1", lastEvent.GetExecutionStalled().GetDescription())
	}
	require.False(t, completed.Load())

	disconnectWorkers(t, ctx, cancelWorkers)
	workerCtx, cancelWorkers = context.WithCancel(ctx)
	t.Cleanup(cancelWorkers)
	newWorkers(t, workerCtx, "v1", p.opts.Recovery)

	for i := range observers {
		wf.WaitForRuntimeStatus(t, ctx, observers[i], id, protos.OrchestrationStatus_ORCHESTRATION_STATUS_COMPLETED)
	}
	require.True(t, completed.Load())
}
