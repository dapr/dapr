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
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/framework/process/placement"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/process/sqlite"

	kitstrings "github.com/dapr/kit/strings"
)

type Actors struct {
	app   *app.App
	db    *sqlite.SQLite
	place *placement.Placement
	sched *scheduler.Scheduler
	daprd *daprd.Daprd

	// sharedControlPlane is true when this Actors instance received its
	// placement, scheduler, and db from an external source.
	// When true, Cleanup skips these shared resources so the owner can
	// shut them down in its own lifecycle.
	sharedControlPlane bool

	runOnce     sync.Once
	cleanupOnce sync.Once
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
	}
	for _, fopt := range fopts {
		fopt(&opts)
	}

	// Tests which pick a topology, or drive the placement service, keep
	// their choice.
	if SchedulerPlacementFromEnv() &&
		!opts.placementService && opts.placement == nil && !opts.schedulerPlacement {
		opts.schedulerPlacement = true
	}

	if opts.scheduler == nil {
		sopts := []scheduler.Option{scheduler.WithID("dapr-scheduler-0")}
		if opts.schedulerPlacement {
			sopts = append(sopts, scheduler.WithPlacementEnabled(true))
		}
		opts.scheduler = scheduler.New(t, sopts...)
	}

	// No standalone placement process runs when placement is served by the
	// scheduler.
	if opts.placement == nil && !opts.schedulerPlacement {
		opts.placement = placement.New(t)
	}

	handlers := make([]app.Option, 0, len(opts.actorTypeHandlers))
	for atype, handler := range opts.actorTypeHandlers {
		handlers = append(handlers, app.WithHandlerFunc("/actors/"+atype+"/", handler))
	}
	for pattern, handler := range opts.handlers {
		handlers = append(handlers, app.WithHandlerFunc(pattern, handler))
	}

	config := fmt.Sprintf(`{"entities": [%s]`, strings.Join(opts.types, ","))
	if opts.reentryMaxDepth != nil {
		require.NotNil(t, opts.reentry)
		config += fmt.Sprintf(`,"reentrancy":{"enabled":%t,"maxStackDepth":%d}`, *opts.reentry, *opts.reentryMaxDepth)
	} else if opts.reentry != nil {
		config += fmt.Sprintf(`,"reentrancy":{"enabled":%t}`, *opts.reentry)
	}

	if opts.actorIdleTimeout != nil {
		config += fmt.Sprintf(`,"actorIdleTimeout":"%s"`, *opts.actorIdleTimeout)
	}

	if opts.drainOngoingCallTimeout != nil {
		config += fmt.Sprintf(`,"drainOngoingCallTimeout":"%s"`, *opts.drainOngoingCallTimeout)
	}

	if opts.drainRebalancedActors != nil {
		config += fmt.Sprintf(`,"drainRebalancedActors":%t`, *opts.drainRebalancedActors)
	}

	if len(opts.entityConfig) > 0 {
		b, err := json.Marshal(opts.entityConfig)
		require.NoError(t, err)
		config += `,"entitiesConfig":` + string(b)
	}

	config += "}"

	app := app.New(t, append(handlers, app.WithConfig(config))...)

	dopts := []daprd.Option{
		daprd.WithAppPort(app.Port()),
		daprd.WithResourceFiles(opts.db.GetComponent(t)),
		daprd.WithConfigManifests(t, opts.daprdConfigs...),
		daprd.WithScheduler(opts.scheduler),
		daprd.WithResourceFiles(opts.resources...),
		daprd.WithErrorCodeMetrics(t),
	}

	if opts.placement != nil {
		dopts = append(dopts, daprd.WithPlacementAddresses(opts.placement.Address()))
	}

	if opts.maxBodySize != nil {
		dopts = append(dopts, daprd.WithMaxBodySize(*opts.maxBodySize))
	}

	dopts = append(dopts, opts.daprdOpts...)

	return &Actors{
		app:                app,
		db:                 opts.db,
		place:              opts.placement,
		sched:              opts.scheduler,
		daprd:              daprd.New(t, dopts...),
		sharedControlPlane: opts.sharedControlPlane,
	}
}

func (a *Actors) Run(t *testing.T, ctx context.Context) {
	a.runOnce.Do(func() {
		a.app.Run(t, ctx)
		a.db.Run(t, ctx)
		if a.place != nil {
			a.place.Run(t, ctx)
		}
		a.sched.Run(t, ctx)
		a.daprd.Run(t, ctx)
	})
}

func (a *Actors) Cleanup(t *testing.T) {
	a.cleanupOnce.Do(func() {
		a.daprd.Cleanup(t)
		if !a.sharedControlPlane {
			a.sched.Cleanup(t)
			if a.place != nil {
				a.place.Cleanup(t)
			}
			a.db.Cleanup(t)
		}
		a.app.Cleanup(t)
	})
}

func (a *Actors) WaitUntilRunning(t *testing.T, ctx context.Context) {
	if a.place != nil {
		a.place.WaitUntilRunning(t, ctx)
	}
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
	return a.daprd.Metrics(t, ctx).All()
}

// Placement returns the standalone placement process. Nil when the actors
// were built with WithSchedulerPlacement, which runs no placement process.
func (a *Actors) Placement() *placement.Placement {
	return a.place
}

// PlacementTables returns the placement table state of the active placement
// authority. Under scheduler placement the state is read with a one-off
// typeless report stream, whose snapshot carries every table of the default
// namespace without joining any of them: Version is the scheduler's
// dissemination count, which advances when any table changes and holds
// still otherwise, and APIVLevel carries the fixed level every current
// daprd reports.
func (a *Actors) PlacementTables(t *testing.T, ctx context.Context) *placement.TableState {
	t.Helper()

	if a.place != nil {
		return a.place.PlacementTables(t, ctx)
	}

	state, err := a.schedulerTables(ctx)
	if err != nil {
		return new(placement.TableState)
	}
	return state
}

// schedulerTables takes one snapshot of the scheduler's placement tables.
func (a *Actors) schedulerTables(ctx context.Context) (*placement.TableState, error) {
	sctx, cancel := context.WithTimeout(ctx, time.Second*10)
	defer cancel()

	metrics, err := a.schedulerMetrics(sctx)
	if err != nil {
		return nil, err
	}

	// No connected sidecar means no namespace, mirroring the placement
	// service. The scheduler also withholds its placement leader without a
	// capable sidecar, so no snapshot could be read.
	if metrics["dapr_scheduler_sidecars_connected"] == 0 {
		return &placement.TableState{Tables: make(map[string]*placement.Table)}, nil
	}

	//nolint:staticcheck
	conn, err := grpc.DialContext(sctx, a.sched.Address(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), grpc.WithReturnConnectionError(),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	stream, err := schedulerv1pb.NewSchedulerClient(conn).ReportActorTypes(sctx)
	if err != nil {
		return nil, err
	}
	if err = stream.Send(&schedulerv1pb.ReportActorTypesRequest{
		Msg: &schedulerv1pb.ReportActorTypesRequest_Report{Report: &schedulerv1pb.ActorHost{
			Address:   "127.0.0.1:1",
			AppId:     "placement-table-reader",
			Namespace: "default",
		}},
	}); err != nil {
		return nil, err
	}

	table := new(placement.Table)
	hosts := make(map[string]*placement.Host)
	for {
		order, oerr := stream.Recv()
		if oerr != nil {
			return nil, oerr
		}
		if serr := stream.Send(&schedulerv1pb.ReportActorTypesRequest{
			Msg: &schedulerv1pb.ReportActorTypesRequest_Ack{Ack: &schedulerv1pb.PlacementOrderAck{
				Operation: order.GetOperation(),
				Seq:       order.GetSeq(),
			}},
		}); serr != nil {
			return nil, serr
		}

		if order.GetOperation() == schedulerv1pb.Operation_OPERATION_UPDATE {
			for atype, entry := range order.GetTables().GetEntries() {
				for addr, host := range entry.GetHosts() {
					h, ok := hosts[addr]
					if !ok {
						h = &placement.Host{
							Name:      addr,
							ID:        host.GetAppId(),
							Namespace: "default",
							APIVLevel: 20,
						}
						hosts[addr] = h
					}
					h.Entities = append(h.Entities, atype)
				}
			}
		}
		if order.GetOperation() == schedulerv1pb.Operation_OPERATION_UNLOCK {
			break
		}
	}

	table.Version = uint64(metrics["dapr_scheduler_placement_disseminations_total"])

	for _, addr := range slices.Sorted(maps.Keys(hosts)) {
		host := hosts[addr]
		slices.Sort(host.Entities)
		table.Hosts = append(table.Hosts, *host)
	}

	return &placement.TableState{Tables: map[string]*placement.Table{"default": table}}, nil
}

// schedulerMetrics scrapes the scheduler's metrics endpoint, summing every
// labeled series into its family name.
func (a *Actors) schedulerMetrics(ctx context.Context) (map[string]float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("http://%s/metrics", a.sched.MetricsAddress()), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("metrics endpoint returned %d", resp.StatusCode)
	}

	families, err := new(expfmt.TextParser).TextToMetricFamilies(resp.Body)
	if err != nil {
		return nil, err
	}

	out := make(map[string]float64, len(families))
	for name, family := range families {
		for _, m := range family.GetMetric() {
			if counter := m.GetCounter(); counter != nil {
				out[name] += counter.GetValue()
			}
			if gauge := m.GetGauge(); gauge != nil {
				out[name] += gauge.GetValue()
			}
		}
	}
	return out, nil
}

func (a *Actors) Scheduler() *scheduler.Scheduler {
	return a.sched
}

func (a *Actors) Daprd() *daprd.Daprd {
	return a.daprd
}

func (a *Actors) AppID() string {
	return a.daprd.AppID()
}

func (a *Actors) DB() *sqlite.SQLite {
	return a.db
}

// SchedulerPlacementFromEnv reports whether
// DAPR_INTEGRATION_SCHEDULER_PLACEMENT is set truthy, which has the
// scheduler serve actor placement for every harness built by this package.
func SchedulerPlacementFromEnv() bool {
	return kitstrings.IsTruthy(os.Getenv("DAPR_INTEGRATION_SCHEDULER_PLACEMENT"))
}
