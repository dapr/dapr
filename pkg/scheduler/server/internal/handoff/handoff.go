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

// Package handoff tracks the facts deciding the actor placement authority
// handoff, all derived from live observation so they rebuild whenever the
// observers do:
//
//   - presence: a placement service being deployed. In kubernetes the
//     controller's pod informer reports it, and in every mode the well-known
//     service name and sidecar-reported placement addresses are probed.
//   - gate: which sidecar capabilities are connected to this scheduler.
//     Sidecars connect to every scheduler, so the local view converges on
//     the cluster view.
package handoff

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/status"

	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
)

const (
	// probeConcurrency bounds parallel probes, and probeBudget the time one
	// detection refresh spends probing in total.
	probeConcurrency = 4
	probeBudget      = time.Second * 5
	// absenceConfirmations is how many consecutive sightless refreshes turn
	// a detected placement service absent: a single one can be a probe
	// running out of time or a placement service restarting, so it must not
	// move the authority.
	absenceConfirmations = 3
)

type Options struct {
	// PlacementDNSName, when set, is resolved periodically to detect a
	// deployed placement service even one too old to announce itself. Empty
	// disables the check.
	PlacementDNSName string

	// Security dials sidecar-reported placement addresses with the
	// placement identity when probing them.
	Security security.Handler
}

// Interface is the view of the handoff state consumed by leadership.
type Interface interface {
	// Ready reports whether the first placement detection completed. A
	// freshly started scheduler must not advertise until it has checked
	// once for a placement service.
	Ready() bool
	PlacementPresent() bool
	Advertised() bool
	AnySchedulerPlacementIncapableSidecars() bool
	AnySchedulerPlacementCapableSidecars() bool
	LatchAdvertised()
}

type Handoff struct {
	dnsName    string
	lookupHost func(context.Context, string) ([]string, error)
	probe      func(context.Context, string, spiffeid.ID) bool
	sec        security.Handler
	onChange   atomic.Pointer[func()]

	lock sync.RWMutex
	// podPresent is set while the kubernetes controller's informer sees a
	// placement pod.
	podPresent bool
	// detected is set while the detection sights a placement service, and
	// misses counts the consecutive sightless refreshes since.
	detected bool
	misses   int
	// reqGen counts detection requests and doneGen the requests answered by
	// a completed refresh: while they differ, a just-reported placement
	// address is unprobed and treated as a present placement service.
	reqGen  uint64
	doneGen uint64
	// advertised latches the advertisement so a brief capable dip does not
	// withdraw it. A reappearing placement service resets it.
	advertised bool
	incapable  bool
	capable    bool

	placementAddresses func() []string

	// ready is closed after the first detection refresh, bounding the
	// window in which a restarted scheduler has not yet looked for a
	// placement service.
	ready     chan struct{}
	readyOnce sync.Once

	// detectCh requests an immediate detection refresh, non-blocking.
	detectCh chan struct{}
}

func New(opts Options) *Handoff {
	h := &Handoff{
		dnsName:    opts.PlacementDNSName,
		lookupHost: net.DefaultResolver.LookupHost,
		sec:        opts.Security,
		ready:      make(chan struct{}),
		detectCh:   make(chan struct{}, 1),
	}
	h.probe = h.probeAddress
	return h
}

// Run drives the placement detection until the context ends.
func (h *Handoff) Run(ctx context.Context) error {
	h.refreshDetection(ctx)
	h.completeReady()
	h.fireOnChange()

	for {
		// While an absence awaits confirmation, refresh quickly so the
		// cutover is not delayed by a full interval per confirmation.
		interval := time.Second * 10
		if h.confirmingAbsence() {
			interval = time.Second
		}
		timer := time.NewTimer(interval)

		select {
		case <-ctx.Done():
			timer.Stop()
			h.completeReady()
			return ctx.Err()

		case <-timer.C:
			h.refreshDetection(ctx)

		case <-h.detectCh:
			// Probing right away keeps the withhold decision ahead of the
			// first sidecar acting on the advertisement.
			timer.Stop()
			h.refreshDetection(ctx)
		}
	}
}

func (h *Handoff) confirmingAbsence() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.misses > 0
}

func (h *Handoff) completeReady() {
	h.readyOnce.Do(func() {
		close(h.ready)
	})
}

// Ready reports whether the first placement detection completed.
func (h *Handoff) Ready() bool {
	select {
	case <-h.ready:
		return true
	default:
		return false
	}
}

// SetOnChange registers the callback fired after any handoff fact changes.
func (h *Handoff) SetOnChange(fn func()) {
	h.onChange.Store(&fn)
}

func (h *Handoff) fireOnChange() {
	if fn := h.onChange.Load(); fn != nil {
		(*fn)()
	}
}

// SetKubernetesPresence records whether the kubernetes controller's informer
// sees a placement pod. A reappearing placement service resets the
// advertisement latch, so the next cutover waits for a capable sidecar
// again.
func (h *Handoff) SetKubernetesPresence(present bool) {
	h.lock.Lock()
	changed := h.podPresent != present
	h.podPresent = present
	if changed && present {
		h.advertised = false
	}
	h.lock.Unlock()
	if !changed {
		return
	}
	if !present {
		// The pods are gone: start confirming the DNS and probe absence now
		// rather than on the next idle tick, so the cutover completes in a
		// few confirmation intervals.
		select {
		case h.detectCh <- struct{}{}:
		default:
		}
	}
	h.fireOnChange()
}

// SetLocalCapabilities records which sidecar capabilities are connected to
// this scheduler.
func (h *Handoff) SetLocalCapabilities(incapable, capable bool) {
	h.lock.Lock()
	h.incapable = incapable
	h.capable = capable
	h.lock.Unlock()
	h.fireOnChange()
}

// refreshDetection looks for a placement service through the well-known
// service name and by probing sidecar-reported placement addresses with the
// placement identity.
func (h *Handoff) refreshDetection(ctx context.Context) {
	h.lock.RLock()
	gen := h.reqGen
	h.lock.RUnlock()

	sighted := h.resolveDNS(ctx) || h.probeReportedAddresses(ctx)

	h.lock.Lock()
	prev := h.detected
	if sighted {
		h.misses = 0
		if !h.detected {
			// A reappearing placement service resets the advertisement latch,
			// so the next cutover waits for a capable sidecar again.
			h.advertised = false
		}
		h.detected = true
	} else if h.detected {
		// A sighting flips presence immediately, absence only after
		// consecutive confirmations.
		h.misses++
		if h.misses >= absenceConfirmations {
			h.detected = false
			h.misses = 0
		}
	}
	changed := h.detected != prev || h.doneGen != gen
	h.doneGen = gen
	h.lock.Unlock()
	if changed {
		h.fireOnChange()
	}
}

func (h *Handoff) resolveDNS(ctx context.Context) bool {
	if h.dnsName == "" {
		return false
	}
	lctx, cancel := context.WithTimeout(ctx, time.Second*3)
	defer cancel()
	addrs, err := h.lookupHost(lctx, h.dnsName)
	return err == nil && len(addrs) > 0
}

// probeReportedAddresses dials each sidecar-reported placement address,
// expecting the placement identity, so a placement service outside the
// well-known service name is still detected while it runs.
func (h *Handoff) probeReportedAddresses(ctx context.Context) bool {
	h.lock.RLock()
	source := h.placementAddresses
	h.lock.RUnlock()
	if source == nil || h.sec == nil {
		return false
	}
	addrs := source()
	if len(addrs) == 0 {
		return false
	}

	placementID, err := spiffeid.FromSegments(
		h.sec.ControlPlaneTrustDomain(),
		"ns", h.sec.ControlPlaneNamespace(), "dapr-placement",
	)
	if err != nil {
		return false
	}

	pctx, cancel := context.WithTimeout(ctx, probeBudget)
	defer cancel()
	sem := make(chan struct{}, probeConcurrency)
	found := make(chan struct{}, 1)
	var wg sync.WaitGroup
	for _, addr := range addrs {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
			case <-pctx.Done():
				return
			}
			defer func() { <-sem }()
			if h.probe(pctx, addr, placementID) {
				select {
				case found <- struct{}{}:
				default:
				}
				cancel()
			}
		})
	}
	wg.Wait()
	select {
	case <-found:
		return true
	default:
		return false
	}
}

// probeAddress reports whether an identity-verified connection to the
// address can be established.
func (h *Handoff) probeAddress(ctx context.Context, addr string, placementID spiffeid.ID) bool {
	conn, err := grpc.NewClient(addr, h.sec.GRPCDialOptionMTLS(placementID))
	if err != nil {
		return false
	}
	defer conn.Close()

	dctx, cancel := context.WithTimeout(ctx, time.Second*2)
	defer cancel()
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			break
		}
		if !conn.WaitForStateChange(dctx, state) {
			return false
		}
	}

	// A reachable server must also speak the placement protocol. Closing
	// the stream before any report registers nothing.
	stream, err := v1pb.NewPlacementClient(conn).ReportDaprStatus(dctx)
	if err != nil {
		return isPlacementService(err)
	}
	//nolint:errcheck
	stream.CloseSend()
	_, err = stream.Recv()
	return isPlacementService(err)
}

// isPlacementService reports whether the protocol check's answer marks the
// peer as a placement service. The peer already proved the placement
// identity on the handshake, so only Unimplemented means no: a slow answer
// is still a placement service.
func isPlacementService(err error) bool {
	return status.Code(err) != codes.Unimplemented
}

// SetPlacementAddresses registers the source of the placement addresses the
// connected sidecars were configured with, probed to detect a placement
// service.
func (h *Handoff) SetPlacementAddresses(fn func() []string) {
	h.lock.Lock()
	h.placementAddresses = fn
	h.lock.Unlock()
}

// RequestDetection refreshes the placement detection right away, as the
// reported placement addresses changed. A placement service is treated as
// present until the refresh completes. Non-blocking.
func (h *Handoff) RequestDetection() {
	h.lock.Lock()
	h.reqGen++
	h.lock.Unlock()
	select {
	case h.detectCh <- struct{}{}:
	default:
	}
	h.fireOnChange()
}

// LatchAdvertised records that the advertisement was made with a capable
// sidecar connected.
func (h *Handoff) LatchAdvertised() {
	h.lock.Lock()
	h.advertised = true
	h.lock.Unlock()
}

// PlacementPresent reports whether a placement service exists: the
// kubernetes informer sees a placement pod, the detection sights one, or a
// detection of just-reported placement addresses is still in flight. The
// in-flight presumption only withholds an advertisement not yet made: a
// sidecar reconnect re-reports its addresses, and that must not withdraw a
// standing advertisement, only a confirmed sighting does.
func (h *Handoff) PlacementPresent() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.podPresent || h.detected || (h.reqGen != h.doneGen && !h.advertised)
}

func (h *Handoff) Advertised() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.advertised
}

// AnySchedulerPlacementIncapableSidecars reports whether this scheduler has
// a connected sidecar which cannot take scheduler placement. Sidecars
// connect to every scheduler, so the local view converges on the cluster
// view.
func (h *Handoff) AnySchedulerPlacementIncapableSidecars() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.incapable
}

// AnySchedulerPlacementCapableSidecars reports whether this scheduler has a
// connected sidecar which can take scheduler placement.
func (h *Handoff) AnySchedulerPlacementCapableSidecars() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.capable
}
