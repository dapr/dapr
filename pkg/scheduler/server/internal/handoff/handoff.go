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
	addrPrefix    = "dapr/placement-handoff/addr/"

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

	lock          sync.RWMutex
	present       bool
	stoodDown     bool
	advertised    bool
	gates         map[string]gateEntry
	sightings     map[string]bool
	reportedAddrs map[string]bool
	detectMisses  int

	readyCh chan struct{}
	client  *clientv3.Client

	// localGateCh signals the run loop to publish the latest local
	// capability state, so callers never block.
	localGateLock sync.Mutex
	localGate     gateEntry
	localGateCh   chan struct{}

	latchCh chan struct{}

	pendingAddrLock sync.Mutex
	pendingAddrs    []string
	reportAddrCh    chan struct{}
}

type gateEntry struct {
	incapable bool
	capable   bool
}

func New(opts Options) *Handoff {
	return &Handoff{
		id:            opts.ID,
		etcd:          opts.Etcd,
		dnsName:       opts.PlacementDNSName,
		lookupHost:    net.DefaultResolver.LookupHost,
		sec:           opts.Security,
		gates:         make(map[string]gateEntry),
		sightings:     make(map[string]bool),
		reportedAddrs: make(map[string]bool),
		readyCh:       make(chan struct{}),
		localGateCh:   make(chan struct{}, 1),
		latchCh:       make(chan struct{}, 1),
		reportAddrCh:  make(chan struct{}, 1),
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

	detectTicker := time.NewTicker(time.Second * 10)
	defer detectTicker.Stop()
	h.refreshDetection(ctx, lease.ID)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-detectTicker.C:
			h.refreshDetection(ctx, lease.ID)

		case <-h.reportAddrCh:
			h.pendingAddrLock.Lock()
			pending := h.pendingAddrs
			h.pendingAddrs = nil
			h.pendingAddrLock.Unlock()
			h.lock.Lock()
			for _, addr := range pending {
				h.reportedAddrs[addr] = true
			}
			h.lock.Unlock()
			for _, addr := range pending {
				if _, err := client.Put(ctx, addrPrefix+addr, "reported"); err != nil {
					log.Errorf("Failed to persist reported placement address: %s", err)
				}
			}
			// Probing right away keeps the withhold decision ahead of the
			// first sidecar acting on the advertisement.
			h.refreshDetection(ctx, lease.ID)

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
	case strings.HasPrefix(key, addrPrefix):
		addr := strings.TrimPrefix(key, addrPrefix)
		if deleted {
			delete(h.reportedAddrs, addr)
			return
		}
		h.reportedAddrs[addr] = true
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
	addrs := make([]string, 0, len(h.reportedAddrs))
	for addr := range h.reportedAddrs {
		addrs = append(addrs, addr)
	}
	h.lock.RUnlock()

	if len(addrs) == 0 || h.sec == nil {
		return false
	}

	placementID, err := spiffeid.FromSegments(
		h.sec.ControlPlaneTrustDomain(),
		"ns", h.sec.ControlPlaneNamespace(), "dapr-placement",
	)
	if err != nil {
		return false
	}

	for _, addr := range addrs {
		if h.probeAddress(ctx, addr, placementID) {
			return true
		}
	}
	return false
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
	var err error
	if resolved || probed {
		_, err = h.client.Put(ctx, sightingPrefix+h.id, encodeSighting(resolved, probed), clientv3.WithLease(lease))
	} else {
		_, err = h.client.Delete(ctx, sightingPrefix+h.id)
	}
	if err != nil {
		log.Errorf("Failed to update the placement service sighting: %s", err)
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

// ReportPlacementAddresses persists placement addresses a sidecar was
// configured with, so they can be probed. Non-blocking.
func (h *Handoff) ReportPlacementAddresses(addresses []string) {
	h.lock.RLock()
	var fresh []string
	for _, addr := range addresses {
		if addr != "" && !h.reportedAddrs[addr] {
			fresh = append(fresh, addr)
		}
	}
	h.lock.RUnlock()
	if len(fresh) == 0 {
		return
	}

	h.pendingAddrLock.Lock()
	h.pendingAddrs = append(h.pendingAddrs, fresh...)
	h.pendingAddrLock.Unlock()
	select {
	case h.reportAddrCh <- struct{}{}:
	default:
	}
}

// Announce persists that a placement service exists. It also clears any
// previous stand-down confirmation, since a serving placement service means
// a future cutover must run the handshake again.
func (h *Handoff) Announce(ctx context.Context) error {
	select {
	case <-h.readyCh:
	case <-ctx.Done():
		return ctx.Err()
	}
	_, err := h.client.Txn(ctx).Then(
		clientv3.OpPut(keyPresent, "true"),
		clientv3.OpDelete(keyStoodDown),
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
