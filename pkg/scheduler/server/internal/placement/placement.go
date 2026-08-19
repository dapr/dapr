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

// Package placement implements the actor placement service inside the
// scheduler. Daprd sidecars report their hosted actor types on the
// ReportActorTypes stream to the elected placement leader, which disseminates
// per-actor-type placement tables to all sidecars in the namespace with a
// three phase lock/update/unlock protocol. Nothing is persisted: tables are
// derived entirely from the live streams and rebuilt from scratch on
// leadership change.
package placement

import (
	"context"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/healthz"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/authorizer"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/placement/loops/namespaces"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/events/loop"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.placement")

type Options struct {
	// Enabled serves placement from this scheduler. When false,
	// ReportActorTypes returns Unimplemented, matching a scheduler version
	// without placement support so clients have a single fallback signal.
	Enabled bool

	ID       string
	Security security.Handler
	Healthz  healthz.Healthz

	DisseminateTimeout time.Duration
	CoalesceWindow     time.Duration
}

// Interface is the placement subsystem of the scheduler.
type Interface interface {
	// Run runs the placement subsystem until ctx is done.
	Run(ctx context.Context) error

	// SetLeader marks this scheduler as the placement leader or not. On
	// losing leadership all placement streams are closed; state is rebuilt
	// from new streams on the next leadership grant.
	SetLeader(leader bool)

	// HasPlacementStreams reports whether any sidecar currently takes
	// placement from this scheduler.
	HasPlacementStreams() bool

	// ReportActorTypes serves a single daprd placement stream.
	ReportActorTypes(stream schedulerv1pb.Scheduler_ReportActorTypesServer) error
}

func New(opts Options) Interface {
	p := &placement{
		enabled: opts.Enabled,
		id:      opts.ID,
		htarget: opts.Healthz.AddTarget("scheduler-placement"),
	}

	if opts.Enabled {
		p.authz = authorizer.New(authorizer.Options{
			Security: opts.Security,
		})
		p.nsLoop = namespaces.New(namespaces.Options{
			Authorizer:         p.authz,
			DisseminateTimeout: opts.DisseminateTimeout,
			CoalesceWindow:     opts.CoalesceWindow,
		})
	}

	return p
}

type placement struct {
	enabled bool
	id      string
	htarget healthz.Target

	authz  *authorizer.Authorizer
	nsLoop loop.Interface[loops.EventNamespace]

	leader  atomic.Bool
	running atomic.Bool
	streams atomic.Int64
}

func (p *placement) Run(ctx context.Context) error {
	if !p.enabled {
		p.htarget.Ready()
		defer p.htarget.NotReady()
		<-ctx.Done()
		return ctx.Err()
	}

	log.Infof("Placement enabled on scheduler %q", p.id)

	p.running.Store(true)
	// Readiness is not gated on leadership: followers are healthy, they just
	// reject placement streams with FailedPrecondition.
	p.htarget.Ready()
	defer p.htarget.NotReady()
	defer p.running.Store(false)

	return concurrency.NewRunnerManager(
		p.nsLoop.Run,
		func(ctx context.Context) error {
			<-ctx.Done()
			p.nsLoop.Close(&loops.Shutdown{Error: ctx.Err()})
			return ctx.Err()
		},
	).Run(ctx)
}

func (p *placement) SetLeader(leader bool) {
	if !p.enabled {
		return
	}

	if p.leader.Swap(leader) != leader {
		log.Infof("Placement leadership changed: leader=%t", leader)
	}
	monitoring.RecordPlacementLeaderStatus(leader)

	p.nsLoop.Enqueue(&loops.SetLeader{Leader: leader})
}

// HasPlacementStreams reports whether any sidecar currently takes placement
// from this scheduler.
func (p *placement) HasPlacementStreams() bool {
	return p.streams.Load() > 0
}

func (p *placement) ReportActorTypes(stream schedulerv1pb.Scheduler_ReportActorTypesServer) error {
	if !p.enabled {
		return status.Error(codes.Unimplemented, "placement is not enabled on this scheduler")
	}

	if !p.leader.Load() || !p.running.Load() {
		return status.Errorf(codes.FailedPrecondition, "scheduler %q is not the placement leader", p.id)
	}

	req, err := stream.Recv()
	if err != nil {
		return err
	}

	initial := req.GetReport()
	if initial == nil {
		return status.Error(codes.InvalidArgument, "first message must be a host report")
	}

	if err = p.authz.Host(stream.Context(), initial); err != nil {
		return err
	}

	log.Infof("Received placement stream from namespace=%s appID=%s address=%s",
		initial.GetNamespace(), initial.GetAppId(), initial.GetAddress())

	ctx, cancel := context.WithCancelCause(stream.Context())
	defer cancel(nil)

	monitoring.RecordPlacementStreamsConnected(initial.GetNamespace(), 1)
	p.streams.Add(1)
	defer func() {
		p.streams.Add(-1)
		monitoring.RecordPlacementStreamsConnected(initial.GetNamespace(), -1)
	}()

	p.nsLoop.Enqueue(&loops.ConnAdd{
		Initial: initial,
		Channel: stream,
		Cancel:  cancel,
	})

	<-ctx.Done()

	return context.Cause(ctx)
}
