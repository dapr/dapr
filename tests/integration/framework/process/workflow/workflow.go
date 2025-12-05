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
	baseDopts    []daprd.Option
	opts         options
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
		daprd.WithResourceFiles(db.GetComponent(t)),
	}

	sched := scheduler.New(t)
	baseDopts = append(baseDopts, daprd.WithScheduler(sched))

	workflow := &Workflow{
		taskregistry: make([]*task.TaskRegistry, 0),
		db:           db,
		place:        place,
		sched:        sched,
		daprds:       make([]*daprd.Daprd, 0),
		baseDopts:    baseDopts,
		opts:         opts,
	}

	// Create initial daprd instances
	for range opts.daprds {
		workflow.addDaprd(t)
	}

	return workflow
}

func (w *Workflow) addDaprd(t *testing.T) int {
	index := len(w.daprds)

	dopts := make([]daprd.Option, 0, len(w.baseDopts))
	dopts = append(dopts, w.baseDopts...)

	for _, daprdOpt := range w.opts.daprdOptions {
		if daprdOpt.index == index {
			dopts = append(dopts, daprdOpt.opts...)
		}
	}

	d := daprd.New(t, dopts...)

	w.daprds = append(w.daprds, d)
	w.taskregistry = append(w.taskregistry, task.NewTaskRegistry())

	for _, orch := range w.opts.orchestrators {
		if orch.index == index {
			require.NoError(t, w.taskregistry[index].AddOrchestratorN(orch.name, orch.fn))
		}
	}
	for _, act := range w.opts.activities {
		if act.index == index {
			require.NoError(t, w.taskregistry[index].AddActivityN(act.name, act.fn))
		}
	}
	return index
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

func (w *Workflow) RunNewDaprd(t *testing.T, ctx context.Context) int {
	t.Helper()
	index := w.addDaprd(t)
	w.daprds[index].Run(t, ctx)
	return index
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
			len(w.DaprN(index).GetMetadata(t, ctx).ActorRuntime.ActiveActors), 3)
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
