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

// Package standdown watches the scheduler cluster and stands the placement
// service down once a scheduler signals the actor placement cutover, since a
// placement service still serving would be a second placement authority. It
// also runs the placement side of the handoff handshake: announce on
// connect, then drain and confirm as leader, so the schedulers withhold the
// advertisement until no placement stream remains. Standing down is
// permanent for the life of the process.
package standdown

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/retry"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement.standdown")

// firstObservationTimeout bounds the first observation, so a watch hung on
// a dead connection does not block serving.
const firstObservationTimeout = time.Second * 10

type Options struct {
	// Addresses are the scheduler addresses to watch. Empty disables the
	// watcher and the placement service serves unconditionally.
	Addresses []string
	Security  security.Handler

	// OnStandDown is called when a scheduler signals the cutover.
	OnStandDown func()
	// OnStandUp is called when the schedulers stop serving placement after
	// a stand-down, as on a rollback.
	OnStandUp func()
}

type StandDown struct {
	addresses   []string
	sec         security.Handler
	onStandDown func()
	onStandUp   func()

	active    atomic.Bool
	announced atomic.Bool

	// firstObservation is closed after the first watch attempt completes,
	// so a placement service restarting after a cutover cannot serve before
	// one look at the advertisement. Failure closes it too, since an
	// unreachable scheduler cluster must not block serving.
	firstObservation     chan struct{}
	firstObservationOnce sync.Once
}

func New(opts Options) *StandDown {
	return &StandDown{
		addresses:        opts.Addresses,
		sec:              opts.Security,
		onStandDown:      opts.OnStandDown,
		onStandUp:        opts.OnStandUp,
		firstObservation: make(chan struct{}),
	}
}

// Active reports whether the placement service is standing down.
func (s *StandDown) Active() bool {
	return s.active.Load()
}

// FirstObservation is closed once the first watch attempt completed.
func (s *StandDown) FirstObservation() <-chan struct{} {
	return s.firstObservation
}

// Run watches the schedulers for the cutover signal and stands down on it.
// The watch continues after the stand-down: a rollback shows as no scheduler
// serving placement, and the stand-down is revoked. Unreachable schedulers
// are retried, placement keeps serving until a cutover is actually observed.
func (s *StandDown) Run(ctx context.Context) error {
	if len(s.addresses) == 0 {
		s.completeFirstObservation()
		<-ctx.Done()
		return ctx.Err()
	}

	schedulerID, err := s.schedulerID()
	if err != nil {
		return err
	}

	log.Infof("Watching schedulers %v for a placement leader advertisement", s.addresses)

	timer := time.AfterFunc(firstObservationTimeout, s.completeFirstObservation)
	defer timer.Stop()

	for i := 0; ; i++ {
		s.watch(ctx, s.addresses[i%len(s.addresses)], schedulerID)
		s.completeFirstObservation()

		if ctx.Err() != nil {
			return ctx.Err()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retry.Jitter(time.Second*2, time.Second)):
		}
	}
}

// Confirm reports the completed stand-down to the schedulers, retrying
// until one acknowledges it. Leader only, since the schedulers advertise as
// soon as this arrives.
func (s *StandDown) Confirm(ctx context.Context) {
	if len(s.addresses) == 0 {
		return
	}

	schedulerID, err := s.schedulerID()
	if err != nil {
		log.Errorf("Failed to build scheduler identity for the stand-down confirmation: %s", err)
		return
	}

	for i := 0; ; i++ {
		if s.report(ctx, s.addresses[i%len(s.addresses)], schedulerID, true) {
			log.Info("Stand-down confirmed to the scheduler cluster")
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(retry.Jitter(time.Second*2, time.Second)):
		}
	}
}

func (s *StandDown) schedulerID() (spiffeid.ID, error) {
	return spiffeid.FromSegments(
		s.sec.ControlPlaneTrustDomain(),
		"ns", s.sec.ControlPlaneNamespace(), "dapr-scheduler",
	)
}

func (s *StandDown) completeFirstObservation() {
	s.firstObservationOnce.Do(func() {
		close(s.firstObservation)
	})
}

// watch opens one WatchHosts stream and applies every response until the
// stream fails.
func (s *StandDown) watch(ctx context.Context, address string, schedulerID spiffeid.ID) {
	// Keepalives detect a scheduler which died mid-stream.
	conn, err := grpc.NewClient(address,
		s.sec.GRPCDialOptionMTLS(schedulerID),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:    time.Second * 10,
			Timeout: time.Second * 5,
		}),
	)
	if err != nil {
		return
	}
	defer conn.Close()

	client := schedulerv1pb.NewSchedulerClient(conn)
	stream, err := client.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest))
	if err != nil {
		log.Debugf("Failed to watch scheduler hosts on %s: %s", address, err)
		return
	}

	for {
		resp, err := stream.Recv()
		if err != nil {
			log.Debugf("Scheduler WatchHosts stream to %s ended: %s", address, err)
			return
		}

		cutover, serves := false, false
		for _, host := range resp.GetHosts() {
			cutover = cutover || host.GetLeader() || host.GetPlacementCutoverPending()
			serves = serves || host.GetSchedulerPlacementEnabled()
		}

		switch {
		case cutover && !s.active.Load():
			s.active.Store(true)
			log.Warn("A scheduler signalled the actor placement cutover: this placement service is standing down. Sidecars too old for scheduler placement will be refused: upgrade them.")
			if s.onStandDown != nil {
				s.onStandDown()
			}
		case !serves && len(resp.GetHosts()) > 0 && s.active.Load():
			// The schedulers rolled back: none serves placement, so the
			// stand-down no longer binds.
			s.active.Store(false)
			s.announced.Store(false)
			log.Warn("The schedulers no longer serve actor placement: this placement service is serving again.")
			if s.onStandUp != nil {
				s.onStandUp()
			}
		}
		s.completeFirstObservation()

		if s.active.Load() {
			continue
		}

		// Announce only after a response showing no cutover, since a
		// placement service about to stand down must not clear the
		// stand-down confirmation the schedulers are relying on.
		if !s.announced.Load() {
			if _, aerr := client.ReportPlacementService(ctx, &schedulerv1pb.ReportPlacementServiceRequest{
				StoodDown: false,
			}); aerr != nil {
				log.Debugf("Failed to announce the placement service to scheduler %s: %s", address, aerr)
			} else {
				s.announced.Store(true)
				log.Info("Announced this placement service to the scheduler cluster")
			}
		}
	}
}

func (s *StandDown) report(ctx context.Context, address string, schedulerID spiffeid.ID, stoodDown bool) bool {
	conn, err := grpc.NewClient(address, s.sec.GRPCDialOptionMTLS(schedulerID))
	if err != nil {
		return false
	}
	defer conn.Close()

	_, err = schedulerv1pb.NewSchedulerClient(conn).ReportPlacementService(ctx, &schedulerv1pb.ReportPlacementServiceRequest{
		StoodDown: stoodDown,
	})
	if err != nil {
		log.Debugf("Failed to report placement service state to scheduler %s: %s", address, err)
		return false
	}
	return true
}
