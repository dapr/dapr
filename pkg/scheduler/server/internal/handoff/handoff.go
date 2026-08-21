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

// Package handoff replicates the facts deciding the actor placement
// authority handoff through etcd, so every scheduler acts on the same state
// and the facts survive restarts and leader changes:
//
//   - present: a placement service exists, withholding the placement leader
//     advertisement until stood-down.
//   - stood-down: the placement service drained its streams and permanently
//     refuses new ones, signaling the use of scheduler placement.
//   - advertised: the advertisement became permanent, so an old sidecar
//     connecting later cannot withhold it again.
//   - gate/<scheduler-id>: which sidecar capabilities are connected to each
//     scheduler, leased so a dead scheduler's entry expires.
package handoff

import (
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spiffe/go-spiffe/v2/spiffeid"
	clientv3 "go.etcd.io/etcd/client/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"

	"github.com/dapr/dapr/pkg/scheduler/server/internal/etcd"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.handoff")

const (
	keyPresent    = "dapr/placement-handoff/present"
	keyStoodDown  = "dapr/placement-handoff/stood-down"
	keyAdvertised = "dapr/placement-handoff/advertised"
	gatePrefix    = "dapr/placement-handoff/gate/"

	// probeConcurrency bounds parallel probes, and probeBudget the time one
	// detection refresh spends probing in total.
	probeConcurrency = 4
	probeBudget      = time.Second * 5

	// sightingPrefix keys each scheduler's current sighting of a serving
	// placement service, with the detectors which saw it as the value.
	sightingPrefix = "dapr/placement-handoff/sighting/"

	gateLeaseTTLSeconds = 10

	// dnsClearMisses is how many consecutive polls sighting nothing, with
	// no other scheduler sighting anything either, clear a stale placement
	// announcement.
	dnsClearMisses = 3
)

type Options struct {
	// ID is this scheduler's name, keying its gate entry.
	ID   string
	Etcd etcd.Interface

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
	PlacementPresent() bool
	PlacementStoodDown() bool
	Advertised() bool
	AnySchedulerPlacementIncapableSidecars() bool
	AnySchedulerPlacementCapableSidecars() bool
	LatchAdvertised()
}

type Handoff struct {
	id         string
	etcd       etcd.Interface
	onChange   atomic.Pointer[func()]
	dnsName    string
	lookupHost func(context.Context, string) ([]string, error)
	sec        security.Handler

	lock         sync.RWMutex
	present      bool
	stoodDown    bool
	advertised   bool
	gates        map[string]gateEntry
	sightings    map[string]bool
	detectMisses int
	// lastSighting is the sighting last published, so an unchanged result
	// is not rewritten every refresh.
	lastSighting string

	placementAddresses func() []string

	readyCh chan struct{}
	client  *clientv3.Client

	// localGateCh signals the run loop to publish the latest local
	// capability state, so callers never block.
	localGateLock sync.Mutex
	localGate     gateEntry
	localGateCh   chan struct{}

	latchCh chan struct{}

	// detectCh requests an immediate detection refresh, non-blocking.
	detectCh chan struct{}
}

type gateEntry struct {
	incapable bool
	capable   bool
}

func New(opts Options) *Handoff {
	return &Handoff{
		id:          opts.ID,
		etcd:        opts.Etcd,
		dnsName:     opts.PlacementDNSName,
		lookupHost:  net.DefaultResolver.LookupHost,
		sec:         opts.Security,
		gates:       make(map[string]gateEntry),
		sightings:   make(map[string]bool),
		readyCh:     make(chan struct{}),
		localGateCh: make(chan struct{}, 1),
		latchCh:     make(chan struct{}, 1),
		detectCh:    make(chan struct{}, 1),
	}
}

func (h *Handoff) Run(ctx context.Context) error {
	client, err := h.etcd.Client(ctx)
	if err != nil {
		return err
	}
	h.client = client

	resp, err := client.Get(ctx, "dapr/placement-handoff/", clientv3.WithPrefix())
	if err != nil {
		return err
	}
	h.lock.Lock()
	for _, kv := range resp.Kvs {
		h.applyKV(string(kv.Key), string(kv.Value), false)
	}
	h.lock.Unlock()
	close(h.readyCh)
	h.fireOnChange()

	lease, err := client.Grant(ctx, gateLeaseTTLSeconds)
	if err != nil {
		return err
	}
	defer func() {
		revokeCtx, cancel := context.WithTimeout(context.Background(), time.Second*3)
		defer cancel()
		//nolint:errcheck
		client.Revoke(revokeCtx, lease.ID)
	}()
	keepalive, err := client.KeepAlive(ctx, lease.ID)
	if err != nil {
		return err
	}

	watchCh := client.Watch(ctx, "dapr/placement-handoff/",
		clientv3.WithPrefix(), clientv3.WithRev(resp.Header.Revision+1))

	// Detection probes network addresses, so it runs off the state loop:
	// gate and watch processing never wait on a probe.
	var wg sync.WaitGroup
	detectCtx, detectCancel := context.WithCancel(ctx)
	defer wg.Wait()
	defer detectCancel()
	wg.Go(func() {
		h.runDetection(detectCtx, lease.ID)
	})

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case _, ok := <-keepalive:
			if !ok {
				// The gate entry expires with the lease, matching a scheduler
				// which died outright.
				return ctx.Err()
			}

		case wresp, ok := <-watchCh:
			if !ok {
				return ctx.Err()
			}
			if wresp.Err() != nil {
				return wresp.Err()
			}
			h.lock.Lock()
			for _, ev := range wresp.Events {
				h.applyKV(string(ev.Kv.Key), string(ev.Kv.Value), ev.Type == clientv3.EventTypeDelete)
			}
			h.lock.Unlock()
			h.fireOnChange()

		case <-h.localGateCh:
			h.localGateLock.Lock()
			entry := h.localGate
			h.localGateLock.Unlock()
			if _, err := client.Put(ctx, gatePrefix+h.id, encodeGate(entry), clientv3.WithLease(lease.ID)); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Errorf("Failed to publish sidecar capability state: %s", err)
				h.requestGatePublish()
			}

		case <-h.latchCh:
			if _, err := client.Put(ctx, keyAdvertised, "true"); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				log.Errorf("Failed to persist the placement advertisement latch: %s", err)
				h.LatchAdvertised()
			}
		}
	}
}

// SetOnChange registers the callback fired after any replicated fact
// changes.
func (h *Handoff) SetOnChange(fn func()) {
	h.onChange.Store(&fn)
}

func (h *Handoff) fireOnChange() {
	if fn := h.onChange.Load(); fn != nil {
		(*fn)()
	}
}

// applyKV updates the cached state for one key. Caller holds the write lock.
func (h *Handoff) applyKV(key, value string, deleted bool) {
	switch {
	case key == keyPresent:
		h.present = !deleted
	case key == keyStoodDown:
		h.stoodDown = !deleted
	case key == keyAdvertised:
		h.advertised = !deleted
	case strings.HasPrefix(key, gatePrefix):
		id := strings.TrimPrefix(key, gatePrefix)
		if deleted {
			delete(h.gates, id)
			return
		}
		h.gates[id] = decodeGate(value)
	case strings.HasPrefix(key, sightingPrefix):
		id := strings.TrimPrefix(key, sightingPrefix)
		if deleted {
			delete(h.sightings, id)
			return
		}
		h.sightings[id] = true
	}
}

// SetLocalCapabilities publishes which sidecar capabilities are connected
// to this scheduler, non-blocking with the latest state winning.
func (h *Handoff) SetLocalCapabilities(incapable, capable bool) {
	h.localGateLock.Lock()
	h.localGate = gateEntry{incapable: incapable, capable: capable}
	h.localGateLock.Unlock()
	h.requestGatePublish()
}

func (h *Handoff) requestGatePublish() {
	select {
	case h.localGateCh <- struct{}{}:
	default:
	}
}

// runDetection refreshes the placement detection periodically and on
// request.
func (h *Handoff) runDetection(ctx context.Context, lease clientv3.LeaseID) {
	// A new lease starts with no published sighting.
	h.lock.Lock()
	h.lastSighting = ""
	h.lock.Unlock()

	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	h.refreshDetection(ctx, lease)

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			h.refreshDetection(ctx, lease)

		case <-h.detectCh:
			// Probing right away keeps the withhold decision ahead of the
			// first sidecar acting on the advertisement.
			h.refreshDetection(ctx, lease)
		}
	}
}

// refreshDetection looks for a deployed placement service the schedulers
// were not told about, through the well-known service name and by probing
// sidecar-reported placement addresses with the placement identity. The
// sightings replicate through etcd so every scheduler acts on the same
// view. Once nothing has been sighted for several polls, a stale
// announcement from a placement service removed without standing down is
// cleared so the cutover can proceed.
func (h *Handoff) refreshDetection(ctx context.Context, lease clientv3.LeaseID) {
	resolved := h.resolveDNS(ctx)
	probed := h.probeReportedAddresses(ctx)

	h.lock.Lock()
	if resolved || probed {
		h.detectMisses = 0
	} else {
		h.detectMisses++
	}
	clearStale := h.present && h.detectMisses >= dnsClearMisses && len(h.sightings) == 0
	h.lock.Unlock()

	h.publishSighting(ctx, resolved, probed, lease)

	if clearStale {
		log.Info("No placement service is deployed, clearing the stale announcement of one removed without standing down. The actor placement cutover can proceed.")
		if _, err := h.client.Delete(ctx, keyPresent); err != nil {
			log.Errorf("Failed to clear the stale placement announcement: %s", err)
		}
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
// well-known service name is still detected while it serves.
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
			if h.probeAddress(pctx, addr, placementID) {
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
			return true
		}
		if !conn.WaitForStateChange(dctx, state) {
			return false
		}
	}
}

func (h *Handoff) publishSighting(ctx context.Context, resolved, probed bool, lease clientv3.LeaseID) {
	sighting := ""
	if resolved || probed {
		sighting = encodeSighting(resolved, probed)
	}
	h.lock.Lock()
	unchanged := sighting == h.lastSighting
	h.lastSighting = sighting
	h.lock.Unlock()
	if unchanged {
		return
	}

	var err error
	if sighting != "" {
		_, err = h.client.Put(ctx, sightingPrefix+h.id, sighting, clientv3.WithLease(lease))
	} else {
		_, err = h.client.Delete(ctx, sightingPrefix+h.id)
	}
	if err != nil {
		log.Errorf("Failed to update the placement service sighting: %s", err)
		h.lock.Lock()
		h.lastSighting = ""
		h.lock.Unlock()
	}
}

func encodeSighting(resolved, probed bool) string {
	var parts []string
	if resolved {
		parts = append(parts, "dns")
	}
	if probed {
		parts = append(parts, "probe")
	}
	return strings.Join(parts, " ")
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
// reported placement addresses changed. Non-blocking.
func (h *Handoff) RequestDetection() {
	select {
	case h.detectCh <- struct{}{}:
	default:
	}
}

// Announce persists that a placement service exists. It also clears any
// previous stand-down confirmation and advertisement latch, since a serving
// placement service means a future cutover must run the handshake again.
func (h *Handoff) Announce(ctx context.Context) error {
	select {
	case <-h.readyCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err := h.client.Txn(ctx).Then(
		clientv3.OpPut(keyPresent, "true"),
		clientv3.OpDelete(keyStoodDown),
		clientv3.OpDelete(keyAdvertised),
	).Commit()
	return err
}

// ClearCutoverState clears the stand-down confirmation and advertisement
// latch, so a scheduler starting without placement enabled leaves no state
// from an earlier cutover behind: after a rollback the next cutover must run
// the handshake and the gate again. Without an earlier cutover there is
// nothing to clear.
func ClearCutoverState(ctx context.Context, e etcd.Interface) error {
	client, err := e.Client(ctx)
	if err != nil {
		return err
	}
	_, err = client.Txn(ctx).Then(
		clientv3.OpDelete(keyStoodDown),
		clientv3.OpDelete(keyAdvertised),
	).Commit()
	return err
}

// ConfirmStoodDown persists the completed stand-down.
func (h *Handoff) ConfirmStoodDown(ctx context.Context) error {
	select {
	case <-h.readyCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err := h.client.Put(ctx, keyStoodDown, "true")
	return err
}

// LatchAdvertised persists the advertisement latch.
func (h *Handoff) LatchAdvertised() {
	select {
	case h.latchCh <- struct{}{}:
	default:
	}
}

func (h *Handoff) PlacementPresent() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.present || len(h.sightings) > 0
}

func (h *Handoff) PlacementStoodDown() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.stoodDown
}

func (h *Handoff) Advertised() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	return h.advertised
}

// AnySchedulerPlacementIncapableSidecars reports whether any scheduler in the cluster has a
// connected sidecar which cannot take scheduler placement.
func (h *Handoff) AnySchedulerPlacementIncapableSidecars() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	for _, gate := range h.gates {
		if gate.incapable {
			return true
		}
	}
	return false
}

// AnySchedulerPlacementCapableSidecars reports whether any scheduler in the cluster has a
// connected sidecar which can take scheduler placement.
func (h *Handoff) AnySchedulerPlacementCapableSidecars() bool {
	h.lock.RLock()
	defer h.lock.RUnlock()
	for _, gate := range h.gates {
		if gate.capable {
			return true
		}
	}
	return false
}

func encodeGate(g gateEntry) string {
	var parts []string
	if g.incapable {
		parts = append(parts, "incapable")
	}
	if g.capable {
		parts = append(parts, "capable")
	}
	return strings.Join(parts, " ")
}

func decodeGate(value string) gateEntry {
	var g gateEntry
	for part := range strings.SplitSeq(value, " ") {
		switch part {
		case "incapable":
			g.incapable = true
		case "capable":
			g.capable = true
		}
	}
	return g
}
