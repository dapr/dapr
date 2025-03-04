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
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/iowriter/logger"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
	"github.com/dapr/durabletask-go/client"
	"github.com/dapr/durabletask-go/task"
)

type Workflow struct {
	registry *task.TaskRegistry
	app      *app.App
	db       *sqlite.SQLite
	place    *placement.Placement
	sched    *scheduler.Scheduler
	daprd    *daprd.Daprd

	runOnce     sync.Once
	cleanupOnce sync.Once
}

func New(t *testing.T, fopts ...Option) *Workflow {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	opts := options{
		registry:        task.NewTaskRegistry(),
		enableScheduler: true,
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	app := app.New(t)
	db := sqlite.New(t,
		sqlite.WithActorStateStore(true),
		sqlite.WithCreateStateTables(),
	)
	place := placement.New(t)

	dopts := []daprd.Option{
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(place.Address()),
		daprd.WithResourceFiles(db.GetComponent(t)),
	}

	var sched *scheduler.Scheduler
	if opts.enableScheduler {
		sched = scheduler.New(t)
		dopts = append(dopts,
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

	return &Workflow{
		registry: opts.registry,
		app:      app,
		db:       db,
		place:    place,
		sched:    sched,
		daprd:    daprd.New(t, dopts...),
	}
}

func (w *Workflow) Run(t *testing.T, ctx context.Context) {
	w.runOnce.Do(func() {
		w.app.Run(t, ctx)
		w.db.Run(t, ctx)
		w.place.Run(t, ctx)
		if w.sched != nil {
			w.sched.Run(t, ctx)
		}
		w.daprd.Run(t, ctx)
	})
}

func (w *Workflow) Cleanup(t *testing.T) {
	w.cleanupOnce.Do(func() {
		w.daprd.Cleanup(t)
		if w.sched != nil {
			w.sched.Cleanup(t)
		}
		w.place.Cleanup(t)
		w.db.Cleanup(t)
		w.app.Cleanup(t)
	})
}

func (w *Workflow) WaitUntilRunning(t *testing.T, ctx context.Context) {
	w.place.WaitUntilRunning(t, ctx)
	if w.sched != nil {
		w.sched.WaitUntilRunning(t, ctx)
	}
	w.daprd.WaitUntilRunning(t, ctx)
}

func (w *Workflow) Registry() *task.TaskRegistry {
	return w.registry
}

func (w *Workflow) BackendClient(t *testing.T, ctx context.Context) *client.TaskHubGrpcClient {
	t.Helper()
	backendClient := client.NewTaskHubGrpcClient(w.daprd.GRPCConn(t, ctx), logger.New(t))
	require.NoError(t, backendClient.StartWorkItemListener(ctx, w.registry))
	return backendClient
}

func (w *Workflow) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	t.Helper()
	return w.daprd.GRPCClient(t, ctx)
}

func (w *Workflow) Dapr() *daprd.Daprd {
	return w.daprd
}

func (w *Workflow) Metrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()
	return w.daprd.Metrics(t, ctx).All()
}

func (w *Workflow) DB() *sqlite.SQLite {
	return w.db
}
