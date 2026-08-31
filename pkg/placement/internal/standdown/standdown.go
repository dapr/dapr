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
// placement service still serving would be a second placement authority.
// Every placement replica holds one state-reporting stream to every
// scheduler, so a live stream is this placement service's presence. The
// stand-down holds until a rollback is observed.
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

	active atomic.Bool

	// mu serializes stand-down transitions with the latest observation:
	// observed is set once a scheduler answered, serves records whether
	// that answer showed a scheduler serving placement. It also guards the
	// state-change broadcast and the stand-down confirmation channel.
	mu       sync.Mutex
	observed bool
	serves   bool
	// stateChanged is closed and replaced whenever the reported state
	// changes, waking every report stream to resend.
	stateChanged chan struct{}
	// confirmed is closed once any scheduler acknowledged a stood-down
	// report, and replaced on a stand-up so a later stand-down waits for a
	// fresh acknowledgment.
	confirmed chan struct{}

	// managers tracks one report stream per scheduler address, reconciled
	// against the bootstrap addresses and the hosts the schedulers
	// broadcast.
	managersLock sync.Mutex
	managers     map[string]context.CancelFunc
	managersWG   sync.WaitGroup

	// firstObservation is closed after the first watch attempt completes,
	// so a placement service restarting after a cutover checks whether the
	// schedulers advertise placement before it serves. Failure closes it
	// too, since an unreachable scheduler cluster must not block serving.
	firstObservation     chan struct{}
	firstObservationOnce sync.Once
}

func New(opts Options) *StandDown {
	return &StandDown{
		addresses:        opts.Addresses,
		sec:              opts.Security,
		onStandDown:      opts.OnStandDown,
		onStandUp:        opts.OnStandUp,
		stateChanged:     make(chan struct{}),
		confirmed:        make(chan struct{}),
		managers:         make(map[string]context.CancelFunc),
		firstObservation: make(chan struct{}),
	}
}

// Active reports whether the placement service is standing down.
func (s *StandDown) Active() bool {
	return s.active.Load()
}

// Inherit records a stand-down committed before this process served, so a
// rollback revokes it like one this watcher observed. It returns false when
// the schedulers were already observed not serving placement: the stand-down
// is stale and the caller revokes it instead.
func (s *StandDown) Inherit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.observed && !s.serves {
		return false
	}
	s.active.Store(true)
	s.notifyStateLocked()
	return true
}

// FirstObservation is closed once the first watch attempt completed.
func (s *StandDown) FirstObservation() <-chan struct{} {
	return s.firstObservation
}

// Run watches the schedulers for the cutover signal and stands down on it,
// while maintaining one state-reporting stream per scheduler. The watch
// continues after the stand-down: a rollback shows as no scheduler serving
// placement, and the stand-down is revoked. Unreachable schedulers are
// retried, placement keeps serving until a cutover is actually observed.
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

	defer s.managersWG.Wait()
	s.reconcileManagers(ctx, s.addresses, schedulerID)

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

// Confirm blocks until a scheduler acknowledged the stood-down report the
// streams carry, since the schedulers advertise as soon as it arrives.
// Leader only.
func (s *StandDown) Confirm(ctx context.Context) {
	if len(s.addresses) == 0 {
		return
	}

	s.mu.Lock()
	confirmed := s.confirmed
	s.notifyStateLocked()
	s.mu.Unlock()

	select {
	case <-confirmed:
		log.Info("Stand-down confirmed to the scheduler cluster")
	case <-ctx.Done():
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

// notifyStateLocked wakes every report stream to resend the current state.
// Caller holds mu.
func (s *StandDown) notifyStateLocked() {
	close(s.stateChanged)
	s.stateChanged = make(chan struct{})
}

// markConfirmed records that a scheduler acknowledged a stood-down report.
func (s *StandDown) markConfirmed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.confirmed:
	default:
		close(s.confirmed)
	}
}

// reconcileManagers keeps one report stream manager per address: the
// bootstrap addresses plus the hosts the schedulers currently broadcast.
func (s *StandDown) reconcileManagers(ctx context.Context, hostAddrs []string, schedulerID spiffeid.ID) {
	want := make(map[string]struct{}, len(s.addresses)+len(hostAddrs))
	for _, addr := range s.addresses {
		want[addr] = struct{}{}
	}
	for _, addr := range hostAddrs {
		if addr != "" {
			want[addr] = struct{}{}
		}
	}

	s.managersLock.Lock()
	defer s.managersLock.Unlock()
	for addr, cancel := range s.managers {
		if _, ok := want[addr]; !ok {
			cancel()
			delete(s.managers, addr)
		}
	}
	for addr := range want {
		if _, ok := s.managers[addr]; ok {
			continue
		}
		mctx, cancel := context.WithCancel(ctx)
		s.managers[addr] = cancel
		s.managersWG.Go(func() {
			s.manage(mctx, addr, schedulerID)
		})
	}
}

// manage maintains the report stream to one scheduler for the life of its
// context.
func (s *StandDown) manage(ctx context.Context, address string, schedulerID spiffeid.ID) {
	for {
		s.reportStream(ctx, address, schedulerID)
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(retry.Jitter(time.Second*2, time.Second)):
		}
	}
}

// reportStream holds one ReportPlacementService stream: it reports the
// current state on connect, resends it on every change, and reads the
// acknowledgments.
func (s *StandDown) reportStream(ctx context.Context, address string, schedulerID spiffeid.ID) {
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

	// WaitForReady reports to a still-starting scheduler the moment it
	// listens, rather than after a retry backoff.
	stream, err := schedulerv1pb.NewSchedulerClient(conn).ReportPlacementService(ctx, grpc.WaitForReady(true))
	if err != nil {
		log.Debugf("Failed to open the placement report stream to scheduler %s: %s", address, err)
		return
	}

	sent := s.active.Load()
	if err := stream.Send(&schedulerv1pb.ReportPlacementServiceRequest{StoodDown: sent}); err != nil {
		return
	}

	for {
		if _, err := stream.Recv(); err != nil {
			log.Debugf("Placement report stream to scheduler %s ended: %s", address, err)
			return
		}
		if sent {
			s.markConfirmed()
		}

		if !s.waitStateChange(ctx, sent) {
			return
		}
		sent = s.active.Load()
		if err := stream.Send(&schedulerv1pb.ReportPlacementServiceRequest{StoodDown: sent}); err != nil {
			return
		}
	}
}

// waitStateChange blocks until the reported state differs from sent,
// returning false when the context ends first.
func (s *StandDown) waitStateChange(ctx context.Context, sent bool) bool {
	for {
		s.mu.Lock()
		changed := s.stateChanged
		s.mu.Unlock()
		if s.active.Load() != sent {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-changed:
		}
	}
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
	stream, err := client.WatchHosts(ctx, new(schedulerv1pb.WatchHostsRequest), grpc.WaitForReady(true))
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
		hostAddrs := make([]string, 0, len(resp.GetHosts()))
		for _, host := range resp.GetHosts() {
			cutover = cutover || host.GetLeader() || host.GetPlacementCutoverPending()
			serves = serves || host.GetSchedulerPlacementEnabled()
			hostAddrs = append(hostAddrs, host.GetAddress())
		}

		s.mu.Lock()
		s.observed = true
		s.serves = serves
		var transition func()
		switch {
		case cutover && !s.active.Load():
			s.active.Store(true)
			s.notifyStateLocked()
			transition = s.onStandDown
			log.Warn("A scheduler signalled the actor placement cutover: this placement service is standing down. Sidecars too old for scheduler placement will be refused: upgrade them.")
		case !serves && len(resp.GetHosts()) > 0 && s.active.Load():
			// The schedulers rolled back: none serves placement, so the
			// stand-down no longer binds. A later stand-down waits for a
			// fresh acknowledgment.
			s.active.Store(false)
			s.notifyStateLocked()
			select {
			case <-s.confirmed:
				s.confirmed = make(chan struct{})
			default:
			}
			transition = s.onStandUp
			log.Warn("The schedulers no longer serve actor placement: this placement service is serving again.")
		}
		s.mu.Unlock()
		if transition != nil {
			transition()
		}
		s.completeFirstObservation()

		// Every scheduler replica learns this placement service's state
		// first hand, so the report streams follow the broadcast host list.
		s.reconcileManagers(ctx, hostAddrs, schedulerID)
	}
}
