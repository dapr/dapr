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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

type Workflow struct {
	taskregistry atomic.Value // map[int]*task.TaskRegistry
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
		enableScheduler: true,
		daprds:          1,
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

	var sched *scheduler.Scheduler
	if opts.enableScheduler {
		sched = scheduler.New(t)
		baseDopts = append(baseDopts,
			daprd.WithScheduler(sched),
			daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: appconfig
spec:
  features:
  - name: SchedulerReminders
    enabled: true
`))
	}

	daprds := make([]*daprd.Daprd, opts.daprds, opts.daprds)

	for i := range daprds {
		for _, daprdOpt := range opts.daprdOptions {
			if daprdOpt.index == i {
				baseDopts = append(baseDopts, daprdOpt.opts...)
				break
			}
		}

		daprds[i] = daprd.New(t, baseDopts...)
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
		taskregistry: atomic.Value{},
		db:           db,
		place:        place,
		sched:        sched,
		daprds:       daprds,
	}

	workflow.taskregistry.Store(registries)
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

// Registry returns the registry for a specific index
func (w *Workflow) Registry(index int) *task.TaskRegistry {
	registries := w.taskregistry.Load().(map[int]*task.TaskRegistry)
	return registries[index]
}

// BackendClient returns a backend client for the specified index
func (w *Workflow) BackendClient(t *testing.T, ctx context.Context, index int) *client.TaskHubGrpcClient {
	t.Helper()
	require.Less(t, index, len(w.daprds), "index out of range")

	backendClient := client.NewTaskHubGrpcClient(w.daprds[index].GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, w.Registry(index)))
	return backendClient
}

// GRPCClientForApp returns a GRPC client for the specified app index
func (w *Workflow) GRPCClient(t *testing.T, ctx context.Context, index int) rtv1.DaprClient {
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
