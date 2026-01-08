/*
Copyright 2024 The Dapr Authors
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
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
	"github.com/dapr/durabletask-go/workflow"
)

type Workflow struct {
	taskregistry []*task.TaskRegistry
	db           *sqlite.SQLite
	place        *placement.Placement
	sched        *scheduler.Scheduler
	daprds       []*daprd.Daprd
}

func New(t *testing.T, fopts ...Option) *Workflow {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	opts := options{
		daprds: 1,
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	require.GreaterOrEqual(t, opts.daprds, 1, "at least one daprd instance is required")

	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	place := placement.New(t)

	baseDopts := []daprd.Option{
		daprd.WithPlacementAddresses(place.Address()),
	}

	if !opts.skipDB {
		baseDopts = append(baseDopts, daprd.WithResourceFiles(db.GetComponent(t)))
	}

	sched := scheduler.New(t)
	baseDopts = append(baseDopts, daprd.WithScheduler(sched))

	daprds := make([]*daprd.Daprd, opts.daprds, opts.daprds)

	for i := range daprds {
		dopts := make([]daprd.Option, 0, len(baseDopts))
		dopts = append(dopts, baseDopts...)

		// Add specific opts for this daprd
		for _, daprdOpt := range opts.daprdOptions {
			if daprdOpt.index == i {
				dopts = append(dopts, daprdOpt.opts...)
			}
		}

		daprds[i] = daprd.New(t, dopts...)
	}

	registries := make(map[int]*task.TaskRegistry)
	for i := range daprds {
		registries[i] = task.NewTaskRegistry()
	}

	// Apply orchestrators & activities to the registry
	for _, orch := range opts.orchestrators {
		if orch.index < len(daprds) {
			require.NoError(t, registries[orch.index].AddOrchestratorN(orch.name, orch.fn))
		}
	}
	for _, act := range opts.activities {
		if act.index < len(daprds) {
			require.NoError(t, registries[act.index].AddActivityN(act.name, act.fn))
		}
	}

	workflow := &Workflow{
		taskregistry: make([]*task.TaskRegistry, len(daprds)),
		db:           db,
		place:        place,
		sched:        sched,
		daprds:       daprds,
	}

	for i := range workflow.taskregistry {
		workflow.taskregistry[i] = registries[i]
	}

	return workflow
}

func (w *Workflow) Run(t *testing.T, ctx context.Context) {
	w.db.Run(t, ctx)
	w.place.Run(t, ctx)
	if w.sched != nil {
		w.sched.Run(t, ctx)
	}
	for _, daprd := range w.daprds {
		daprd.Run(t, ctx)
	}
}

func (w *Workflow) Cleanup(t *testing.T) {
	for _, daprd := range w.daprds {
		daprd.Cleanup(t)
	}
	if w.sched != nil {
		w.sched.Cleanup(t)
	}
	w.place.Cleanup(t)
	w.db.Cleanup(t)
}

func (w *Workflow) WaitUntilRunning(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)
	if w.sched != nil {
		w.sched.WaitUntilRunning(t, ctx)
	}
	for _, daprd := range w.daprds {
		daprd.WaitUntilRunning(t, ctx)
	}
}

func (w *Workflow) Registry() *task.TaskRegistry {
	return w.taskregistry[0]
}

// Registry returns the registry for a specific index
func (w *Workflow) RegistryN(index int) *task.TaskRegistry {
	return w.taskregistry[index]
}

func (w *Workflow) WorkflowClient(t *testing.T, ctx context.Context) *workflow.Client {
	t.Helper()
	return workflow.NewClient(w.Dapr().GRPCConn(t, ctx))
}

func (w *Workflow) WorkflowClientN(t *testing.T, ctx context.Context, index int) *workflow.Client {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")
	return workflow.NewClient(w.DaprN(index).GRPCConn(t, ctx))
}

func (w *Workflow) BackendClient(t *testing.T, ctx context.Context) *client.TaskHubGrpcClient {
	t.Helper()

	return w.BackendClientN(t, ctx, 0)
}

// BackendClient returns a backend client for the specified index
func (w *Workflow) BackendClientN(t *testing.T, ctx context.Context, index int) *client.TaskHubGrpcClient {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")

	backendClient := client.NewTaskHubGrpcClient(w.daprds[index].GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, w.RegistryN(index)))

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.GreaterOrEqual(c,
			len(w.Dapr().GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
	}, time.Second*10, time.Millisecond*10)

	return backendClient
}

func (w *Workflow) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	t.Helper()
	return w.daprds[0].GRPCClient(t, ctx)
}

// GRPCClientForApp returns a GRPC client for the specified app index
func (w *Workflow) GRPCClientN(t *testing.T, ctx context.Context, index int) rtv1.DaprClient {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")
	return w.daprds[index].GRPCClient(t, ctx)
}

func (w *Workflow) Dapr() *daprd.Daprd {
	return w.daprds[0]
}

func (w *Workflow) DaprN(i int) *daprd.Daprd {
	return w.daprds[i]
}

func (w *Workflow) Metrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()
	return w.daprds[0].Metrics(t, ctx).All()
}

func (w *Workflow) DB() *sqlite.SQLite {
	return w.db
}

func (w *Workflow) Scheduler() *scheduler.Scheduler {
	return w.sched
}
