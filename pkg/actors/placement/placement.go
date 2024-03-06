/*
Copyright 2021 The Dapr Authors
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

package placement

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"golang.org/x/exp/maps"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/dapr/pkg/actors/internal"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/dapr/dapr/pkg/placement/raft"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.runtime.actors.placement")

const (
	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"

	// Interval to wait for app health's readiness
	placementReadinessWaitInterval = 500 * time.Millisecond
	// Minimum and maximum reconnection intervals
	placementReconnectMinInterval = 1 * time.Second
	placementReconnectMaxInterval = 30 * time.Second
	statusReportHeartbeatInterval = 1 * time.Second

	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
)

// actorPlacement maintains membership of actor instances and consistent hash
// tables to discover the actor while interacting with Placement service.
type actorPlacement struct {
	actorTypes []string
	config     internal.Config

	// client is the placement client.
	client *placementClient

	// serverAddr is the list of placement addresses.
	serverAddr []string
	// serverIndex is the current index of placement servers in serverAddr.
	serverIndex atomic.Int32

	// placementTables is the consistent hashing table map to
	// look up Dapr runtime host address to locate actor.
	placementTables *hashing.ConsistentHashTables
	// placementTableLock is the lock for placementTables.
	placementTableLock sync.RWMutex
	// hasPlacementTablesCh is closed when the placement tables have been received.
	hasPlacementTablesCh chan struct{}

	// apiLevel is the current API level of the cluster
	apiLevel uint32
	// onAPILevelUpdate is invoked when the API level is updated
	onAPILevelUpdate func(apiLevel uint32)

	// unblockSignal is the channel to unblock table locking.
	unblockSignal chan struct{}
	// operationUpdateLock is the lock for three stage commit.
	operationUpdateLock sync.Mutex

	// appHealthFn returns the appHealthCh
	appHealthFn internal.AppHealthFn
	// appHealthy contains the result of the app health checks.
	appHealthy atomic.Bool
	// afterTableUpdateFn is the function invoked after table updates,
	// such as draining actors and resetting reminders.
	afterTableUpdateFn func()

	// callback invoked to halt all active actors
	haltAllActorsFn internal.HaltAllActorsFn

	// running is the flag when runtime is running.
	running atomic.Bool
	// shutdownConnLoop is the wait group to wait until all connection loop are done
	shutdownConnLoop sync.WaitGroup
	// closeCh is the channel to close the placement service.
	closeCh chan struct{}

	resiliency resiliency.Provider

	virtualNodesCache *hashing.VirtualNodesCache
}

// NewActorPlacement initializes ActorPlacement for the actor service.
func NewActorPlacement(opts internal.ActorsProviderOptions) internal.PlacementService {
	addrs := utils.ParseServiceAddr(strings.TrimPrefix(opts.Config.ActorsService, "placement:"))
	servers := addDNSResolverPrefix(addrs)
	return &actorPlacement{
		config:     opts.Config,
		serverAddr: servers,

		client:          newPlacementClient(getGrpcOptsGetter(servers, opts.Security)),
		placementTables: &hashing.ConsistentHashTables{Entries: make(map[string]*hashing.Consistent)},

		actorTypes:        []string{},
		unblockSignal:     make(chan struct{}, 1),
		appHealthFn:       opts.AppHealthFn,
		closeCh:           make(chan struct{}),
		resiliency:        opts.Resiliency,
		virtualNodesCache: hashing.NewVirtualNodesCache(),
	}
}

func (p *actorPlacement) PlacementHealthy() bool {
	return p.appHealthy.Load() && p.client.isConnected()
}

func (p *actorPlacement) StatusMessage() string {
	if p.client.isConnected() {
		return "placement: connected"
	}
	return "placement: disconnected"
}

// Register an actor type by adding it to the list of known actor types (if it's not already registered)
// The placement tables will get updated when the next heartbeat fires
func (p *actorPlacement) AddHostedActorType(actorType string, idleTimeout time.Duration) error {
	for _, t := range p.actorTypes {
		if t == actorType {
			return fmt.Errorf("actor type %s already registered", actorType)
		}
	}

	p.actorTypes = append(p.actorTypes, actorType)
	return nil
}

// Start connects placement service to register to membership and send heartbeat
// to report the current member status periodically.
func (p *actorPlacement) Start(ctx context.Context) error {
	p.serverIndex.Store(0)
	p.running.Store(true)
	p.appHealthy.Store(true)
	p.resetPlacementTables()

	if !p.establishStreamConn(ctx) {
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()

		select {
		case <-ctx.Done():
		case <-p.closeCh:
		}
		cancel()
	}()

	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		ch := p.appHealthFn(ctx)
		if ch == nil {
			return
		}

		for healthy := range ch {
			p.appHealthy.Store(healthy)
		}
	}()

	// establish connection loop, whenever a disconnect occurs it starts to run trying to connect to a new server.
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		for p.running.Load() {
			// wait until disconnection occurs or shutdown is triggered
			p.client.waitUntil(func(streamConnAlive bool) bool {
				return !streamConnAlive || !p.running.Load()
			})

			if !p.running.Load() {
				break
			}
			p.establishStreamConn(ctx)
		}
	}()

	// Establish receive channel to retrieve placement table update
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		for p.running.Load() {
			// Wait until stream is connected or shutdown is triggered.
			p.client.waitUntil(func(streamAlive bool) bool {
				return streamAlive || !p.running.Load()
			})

			resp, err := p.client.recv()
			if !p.running.Load() {
				break
			}

			// TODO: we may need to handle specific errors later.
			if err != nil {
				p.client.disconnectFn(func() {
					p.onPlacementError(err) // depending on the returned error a new server could be used
				})
			} else {
				p.onPlacementOrder(resp)
			}
		}
	}()

	// Send the current host status to placement to register the member and
	// maintain the status of member by placement.
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		for p.running.Load() {
			// Wait until stream is connected or shutdown is triggered.
			p.client.waitUntil(func(streamAlive bool) bool {
				return streamAlive || !p.running.Load()
			})

			if !p.running.Load() {
				break
			}

			// appHealthy is the health status of actor service application. This allows placement to update
			// the member list and hashing table quickly.
			if !p.appHealthy.Load() {
				// app is unresponsive, close the stream and disconnect from the placement service.
				// Then Placement will remove this host from the member list.
				log.Debug("Disconnecting from placement service by the unhealthy app")

				p.client.disconnect()
				p.resetPlacementTables()
				if p.haltAllActorsFn != nil {
					haltErr := p.haltAllActorsFn()
					if haltErr != nil {
						log.Errorf("Failed to deactivate all actors: %v", haltErr)
					}
				}
				continue
			}

			host := v1pb.Host{
				Name:     p.config.GetRuntimeHostname(),
				Entities: p.actorTypes,
				Id:       p.config.AppID,
				Load:     1, // Not used yet
				Pod:      p.config.PodName,
				// Port is redundant because Name should include port number
				// Port: 0,
				ApiLevel: internal.ActorAPILevel,
			}

			err := p.client.send(&host)
			if err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
				log.Errorf("Failed to report status to placement service : %v", err)
			}

			// No delay if stream connection is not alive.
			if p.client.isConnected() {
				diag.DefaultMonitoring.ActorStatusReported("send")
				select {
				case <-time.After(statusReportHeartbeatInterval):
				case <-p.closeCh:
				}
			}
		}
	}()

	return nil
}

// Closes shuts down server stream gracefully.
func (p *actorPlacement) Close() error {
	// CAS to avoid stop more than once.
	if p.running.CompareAndSwap(true, false) {
		p.client.disconnect()
		close(p.closeCh)
	}
	p.shutdownConnLoop.Wait()
	return nil
}

// WaitUntilReady waits until placement table is until table lock is unlocked.
func (p *actorPlacement) WaitUntilReady(ctx context.Context) error {
	p.placementTableLock.RLock()
	hasTablesCh := p.hasPlacementTablesCh
	p.placementTableLock.RUnlock()

	select {
	case p.unblockSignal <- struct{}{}:
		select {
		case <-p.unblockSignal:
		default:
		}
		// continue
	case <-ctx.Done():
		return ctx.Err()
	}

	if hasTablesCh == nil {
		return nil
	}

	select {
	case <-hasTablesCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LookupActor resolves to actor service instance address using consistent hashing table.
func (p *actorPlacement) LookupActor(ctx context.Context, req internal.LookupActorRequest) (internal.LookupActorResponse, error) {
	// Retry here to allow placement table dissemination/rebalancing to happen.
	policyDef := p.resiliency.BuiltInPolicy(resiliency.BuiltInActorNotFoundRetries)
	policyRunner := resiliency.NewRunner[internal.LookupActorResponse](ctx, policyDef)
	return policyRunner(func(ctx context.Context) (res internal.LookupActorResponse, rErr error) {
		rAddr, rAppID, rErr := p.doLookupActor(ctx, req.ActorType, req.ActorID)
		if rErr != nil {
			return res, fmt.Errorf("error finding address for actor %s/%s: %w", req.ActorType, req.ActorID, rErr)
		} else if rAddr == "" {
			return res, fmt.Errorf("did not find address for actor %s/%s", req.ActorType, req.ActorID)
		}
		res.Address = rAddr
		res.AppID = rAppID
		return res, nil
	})
}

func (p *actorPlacement) doLookupActor(ctx context.Context, actorType, actorID string) (string, string, error) {
	p.placementTableLock.RLock()
	defer p.placementTableLock.RUnlock()

	if p.placementTables == nil {
		return "", "", errors.New("placement tables are not set")
	}

	t := p.placementTables.Entries[actorType]
	if t == nil {
		return "", "", nil
	}
	host, err := t.GetHost(actorID)
	if err != nil || host == nil {
		return "", "", nil //nolint:nilerr
	}
	return host.Name, host.AppID, nil
}

//nolint:nosnakecase
func (p *actorPlacement) establishStreamConn(ctx context.Context) (established bool) {
	// Backoff for reconnecting in case of errors
	bo := backoff.NewExponentialBackOff()
	bo.InitialInterval = placementReconnectMinInterval
	bo.MaxInterval = placementReconnectMaxInterval
	bo.MaxElapsedTime = 0 // Retry forever

	logFailureShown := false
	for p.running.Load() {
		// Do not retry to connect if context is canceled
		if ctx.Err() != nil {
			return false
		}

		// Stop reconnecting to placement until app is healthy.
		if !p.appHealthy.Load() {
			// We are not using an exponential backoff here because we haven't begun to establish connections yet
			select {
			case <-p.closeCh:
			case <-time.After(placementReadinessWaitInterval):
			}
			continue
		}

		// Check for context validity again, after sleeping
		if ctx.Err() != nil {
			return false
		}

		serverAddr := p.serverAddr[p.serverIndex.Load()]

		if !logFailureShown {
			log.Debug("try to connect to placement service: " + serverAddr)
		}

		err := p.client.connectToServer(ctx, serverAddr)
		if err == errEstablishingTLSConn {
			return false
		}

		if err != nil {
			if !logFailureShown {
				log.Debugf("Error connecting to placement service (will retry to connect in background): %v", err)
				// Don't show the debug log more than once per each reconnection attempt
				logFailureShown = true
			}

			// Try a different instance of the placement service
			p.serverIndex.Store((p.serverIndex.Load() + 1) % int32(len(p.serverAddr)))

			// Halt all active actors, then reset the placement tables
			if p.haltAllActorsFn != nil {
				p.haltAllActorsFn()
			}
			p.resetPlacementTables()

			// Sleep with an exponential backoff
			select {
			case <-time.After(bo.NextBackOff()):
			case <-ctx.Done():
				return false
			case <-p.closeCh:
				return false
			}
			continue
		}

		log.Debug("Established connection to placement service at " + p.client.clientConn.Target())
		return true
	}

	return false
}

// onPlacementError closes the current placement stream and reestablish the connection again,
// uses a different placement server depending on the error code
func (p *actorPlacement) onPlacementError(err error) {
	s, ok := status.FromError(err)
	// If the current server is not leader, then it will try to the next server.
	if ok && s.Code() == codes.FailedPrecondition {
		p.serverIndex.Store((p.serverIndex.Load() + 1) % int32(len(p.serverAddr)))
	} else {
		log.Debugf("Disconnected from placement: %v", err)
	}
}

func (p *actorPlacement) onPlacementOrder(in *v1pb.PlacementOrder) {
	log.Debugf("Placement order received: %s", in.GetOperation())
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(in.GetOperation())

	// lock all incoming calls when an updated table arrives
	p.operationUpdateLock.Lock()
	defer p.operationUpdateLock.Unlock()

	switch in.GetOperation() {
	case lockOperation:
		p.blockPlacements()

		go func() {
			// TODO: Use lock-free table update.
			// current implementation is distributed two-phase locking algorithm.
			// If placement experiences intermittently outage during updateplacement,
			// user application will face 5 second blocking even if it can avoid deadlock.
			// It can impact the entire system.
			time.Sleep(time.Second * 5)
			p.unblockPlacements()
		}()

	case unlockOperation:
		p.unblockPlacements()

	case updateOperation:
		p.updatePlacements(in.GetTables())
	}
}

func (p *actorPlacement) blockPlacements() {
	select {
	case p.unblockSignal <- struct{}{}:
		// Now  blocked
	default:
		// Was already blocked
	}
}

func (p *actorPlacement) unblockPlacements() {
	select {
	case <-p.unblockSignal:
		// Now unblocked
	default:
		// Was already unblocked
	}
}

// Resets the placement tables.
func (p *actorPlacement) resetPlacementTables() {
	p.placementTableLock.Lock()
	defer p.placementTableLock.Unlock()

	if p.hasPlacementTablesCh != nil {
		close(p.hasPlacementTablesCh)
	}
	p.hasPlacementTablesCh = make(chan struct{})
	maps.Clear(p.placementTables.Entries)
	p.placementTables.Version = ""
}

func (p *actorPlacement) updatePlacements(in *v1pb.PlacementTables) {
	updated := false
	var updatedAPILevel *uint32
	func() {
		p.placementTableLock.RLock()
		if in.GetVersion() == p.placementTables.Version {
			p.placementTableLock.RUnlock()
			return
		}
		p.placementTableLock.RUnlock()

		if in.GetApiLevel() != p.apiLevel {
			p.apiLevel = in.GetApiLevel()
			updatedAPILevel = ptr.Of(in.GetApiLevel())
		}

		entries := map[string]*hashing.Consistent{}

		for k, v := range in.GetEntries() {
			loadMap := make(map[string]*hashing.Host, len(v.GetLoadMap()))
			for lk, lv := range v.GetLoadMap() {
				loadMap[lk] = hashing.NewHost(lv.GetName(), lv.GetId(), lv.GetLoad(), lv.GetPort())
			}

			// TODO: @elena in v1.15 remove the check for versions < 1.13
			// only keep `hashing.NewFromExisting`

			if p.apiLevel < raft.NoVirtualNodesInPlacementTablesAPILevel {
				entries[k] = hashing.NewFromExistingWithVirtNodes(v.GetHosts(), v.GetSortedSet(), loadMap)
			} else {
				entries[k] = hashing.NewFromExisting(loadMap, in.GetReplicationFactor(), p.virtualNodesCache)
			}
		}

		p.placementTableLock.Lock()
		defer p.placementTableLock.Unlock()

		// Check if the table was updated in the meantime
		// This is not needed atm, because the placement leader is the only one sending updates,
		// but it might be needed soon because there's plans to allow other nodes to send updates
		// This is a very cheap check, so it's a good idea to keep it
		if in.GetVersion() == p.placementTables.Version {
			return
		}

		maps.Clear(p.placementTables.Entries)
		p.placementTables.Version = in.GetVersion()
		p.placementTables.Entries = entries

		updated = true
		if p.hasPlacementTablesCh != nil {
			close(p.hasPlacementTablesCh)
			p.hasPlacementTablesCh = nil
		}
	}()

	if updatedAPILevel != nil && p.onAPILevelUpdate != nil {
		p.onAPILevelUpdate(*updatedAPILevel)
	}

	if updated {
		// May call LookupActor inside, so should not do this with placementTableLock locked.
		if p.afterTableUpdateFn != nil {
			p.afterTableUpdateFn()
		}
		log.Infof("Placement tables updated, version: %s", in.GetVersion())
	}
}

func (p *actorPlacement) SetOnTableUpdateFn(fn func()) {
	p.afterTableUpdateFn = fn
}

func (p *actorPlacement) SetOnAPILevelUpdate(fn func(apiLevel uint32)) {
	p.onAPILevelUpdate = fn
}

func (p *actorPlacement) ReportActorDeactivation(ctx context.Context, actorType, actorID string) error {
	// Nop in this implementation
	return nil
}

func (p *actorPlacement) SetHaltActorFns(haltFn internal.HaltActorFn, haltAllFn internal.HaltAllActorsFn) {
	// haltFn isn't used in this implementation
	p.haltAllActorsFn = haltAllFn
	return
}

// addDNSResolverPrefix add the `dns://` prefix to the given addresses
func addDNSResolverPrefix(addr []string) []string {
	resolvers := make([]string, 0, len(addr))
	for _, a := range addr {
		prefix := ""
		host, _, err := net.SplitHostPort(a)
		if err == nil && net.ParseIP(host) == nil {
			prefix = "dns:///"
		}
		resolvers = append(resolvers, prefix+a)
	}
	return resolvers
}
