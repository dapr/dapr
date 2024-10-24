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

package actors

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"google.golang.org/grpc"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"
)

type Actors struct {
	app   *app.App
	db    *sqlite.SQLite
	place *placement.Placement
	sched *scheduler.Scheduler
	daprd *daprd.Daprd
}

func New(t *testing.T, fopts ...Option) *Actors {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("Skipping test on Windows due to SQLite limitations")
	}

	opts := options{
		db: sqlite.New(t,
			sqlite.WithActorStateStore(true),
			sqlite.WithCreateStateTables(),
		),
		placement: placement.New(t),
		scheduler: scheduler.New(t),
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	handlers := make([]app.Option, 0, len(opts.actorTypeHandlers))
	for atype, handler := range opts.actorTypeHandlers {
		handlers = append(handlers, app.WithHandlerFunc("/actors/"+atype+"/", handler))
	}

	app := app.New(t,
		append(handlers,
			app.WithConfig(fmt.Sprintf(`{"entities": [%s]}`, strings.Join(opts.types, ","))),
		)...,
	)

	dopts := []daprd.Option{
		daprd.WithAppPort(app.Port()),
		daprd.WithPlacementAddresses(opts.placement.Address()),
		daprd.WithResourceFiles(opts.db.GetComponent(t)),
		daprd.WithConfigManifests(t, opts.daprdConfigs...),
		daprd.WithScheduler(opts.scheduler),
	}

	return &Actors{
		app:   app,
		db:    opts.db,
		place: opts.placement,
		sched: opts.scheduler,
		daprd: daprd.New(t, dopts...),
	}
}

func (a *Actors) Run(t *testing.T, ctx context.Context) {
	a.app.Run(t, ctx)
	a.db.Run(t, ctx)
	a.place.Run(t, ctx)
	a.sched.Run(t, ctx)
	a.daprd.Run(t, ctx)
}

func (a *Actors) Cleanup(t *testing.T) {
	a.daprd.Cleanup(t)
	a.sched.Cleanup(t)
	a.place.Cleanup(t)
	a.db.Cleanup(t)
	a.app.Cleanup(t)
}

func (a *Actors) WaitUntilRunning(t *testing.T, ctx context.Context) {
	a.place.WaitUntilRunning(t, ctx)
	a.sched.WaitUntilRunning(t, ctx)
	a.daprd.WaitUntilRunning(t, ctx)
}

func (a *Actors) GRPCClient(t *testing.T, ctx context.Context) rtv1.DaprClient {
	t.Helper()
	return a.daprd.GRPCClient(t, ctx)
}

func (a *Actors) GRPCConn(t *testing.T, ctx context.Context) *grpc.ClientConn {
	t.Helper()
	return a.daprd.GRPCConn(t, ctx)
}

func (a *Actors) Metrics(t *testing.T, ctx context.Context) map[string]float64 {
	t.Helper()
	return a.daprd.Metrics(t, ctx)
}

func (a *Actors) Placement() *placement.Placement {
	return a.place
}

func (a *Actors) Scheduler() *scheduler.Scheduler {
	return a.sched
}

func (a *Actors) DB() *sqlite.SQLite {
	return a.db
}
