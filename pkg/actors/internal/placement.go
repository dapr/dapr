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

package internal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	daprCredentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.runtime.actor.internal.placement")

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

type Options struct {
	ServerAddr         []string
	ClientCert         *daprCredentials.CertChain
	AppID              string
	RuntimeHostName    string
	ActorTypes         []string
	AppHealthFn        func() bool
	AfterTableUpdateFn func()
}

// ActorPlacement maintains membership of actor instances and consistent hash
// tables to discover the actor while interacting with Placement service.
//
//nolint:nosnakecase
type ActorPlacement struct {
	actorTypes []string
	appID      string
	// runtimeHostname is the address and port of the runtime
	runtimeHostName string

	// serverAddr is the list of placement addresses.
	serverAddr []string
	// serverIndex is the current index of placement servers in serverAddr.
	serverIndex atomic.Int32

	// placementTables is the consistent hashing table map to
	// look up Dapr runtime host address to locate actor.
	placementTables *hashing.ConsistentHashTables

	// placementTableLock is the lock for placementTables.
	placementTableLock sync.RWMutex

	// unblockSignal is the channel to unblock table locking.
	unblockSignal chan struct{}

	// tableIsBlocked is the status of table lock.
	tableIsBlocked atomic.Bool

	// operationUpdateLock is the lock for three stage commit.
	operationUpdateLock sync.Mutex

	// appHealthFn is the user app health check callback.
	appHealthFn func() bool

	// afterTableUpdateFn is function for post processing done after table updates,
	// such as draining actors and resetting reminders.
	afterTableUpdateFn func()

	// grpcOpts is the gRPC dial options.
	grpcOpts func() ([]grpc.DialOption, error)

	// closedCh signals that the actor placement is closed.
	closedCh chan struct{}

	// shutdown is the flag to indicate if the actor placement is closed.
	shutdown atomic.Bool

	// wg is the wait group for all goroutines.
	wg sync.WaitGroup

	// clock is the clock for time operations, used for testing.
	clock clock.Clock
}

// NewActorPlacement initializes ActorPlacement for the actor service.
func NewActorPlacement(opts Options) *ActorPlacement {
	servers := addDNSResolverPrefix(opts.ServerAddr)
	return &ActorPlacement{
		actorTypes:         opts.ActorTypes,
		appID:              opts.AppID,
		runtimeHostName:    opts.RuntimeHostName,
		placementTables:    &hashing.ConsistentHashTables{Entries: make(map[string]*hashing.Consistent)},
		appHealthFn:        opts.AppHealthFn,
		afterTableUpdateFn: opts.AfterTableUpdateFn,
		clock:              clock.RealClock{},
		serverAddr:         servers,
		grpcOpts:           getGrpcOptsGetter(servers, opts.ClientCert),
		closedCh:           make(chan struct{}),
	}
}

// Register an actor type by adding it to the list of known actor types (if it's not already registered)
// The placement tables will get updated when the next heartbeat fires
func (p *ActorPlacement) AddHostedActorType(actorType string) error {
	p.operationUpdateLock.Lock()
	defer p.operationUpdateLock.Unlock()

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
func (p *ActorPlacement) Start(ctx context.Context) error {
	if p.shutdown.Load() {
		return errors.New("placement is shutdown")
	}

	for {
		if err := p.manageConnectionLoop(ctx); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case <-p.closedCh:
			return nil
		case <-p.clock.Tick(placementReconnectInterval):
		}
	}
}

func (p *ActorPlacement) manageConnectionLoop(ctx context.Context) error {
	if p.shutdown.Load() {
		return nil
	}

	defer p.wg.Wait()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	serverAddr := p.serverAddr[p.serverIndex.Load()]
	log.Infof("trying to connect to placement service: %s", serverAddr)

	client, err := newPlacementClient(ctx, serverAddr, p.grpcOpts)
	if errors.Is(err, errEstablishingTLSConn) || errors.Is(err, context.Canceled) {
		return nil
	}
	if err != nil {
		p.serverIndex.Store((p.serverIndex.Load() + 1) % int32(len(p.serverAddr)))
		return err
	}

	log.Infof("connected to placement service at address %s", serverAddr)

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer client.Close()
		defer cancel()

		for {
			select {
			case <-ctx.Done():
				return
			case <-p.closedCh:
				return
			case <-p.clock.After(statusReportHeartbeatInterval):
			}

			// appHealthFn is the health status of actor service application. This allows placement to update
			// the member list and hashing table quickly.
			if !p.appHealthFn() {
				// app is unresponsive, close the stream and disconnect from the placement service.
				// Then Placement will remove this host from the member list.
				log.Debug("disconnecting from placement service by the unhealthy app.")
				return
			}
		}
	}()

	// Establish receive channel to retrieve placement table update
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer cancel()

		for {
			resp, err := client.Recv()
			if err != nil {
				p.onPlacementError(err)
				return
			}
			p.onPlacementOrder(ctx, resp)
		}
	}()

	for {
		err := client.Send(&v1pb.Host{
			Name:     p.runtimeHostName,
			Entities: p.actorTypes,
			Id:       p.appID,
			Load:     1, // Not used yet
			// Port is redundant because Name should include port number
		})
		if err != nil {
			diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
			log.Debugf("failed to report status to placement service : %v", err)
		}

		diag.DefaultMonitoring.ActorStatusReported("send")

		select {
		case <-p.clock.Tick(statusReportHeartbeatInterval):
		case <-ctx.Done():
			return nil
		}
	}
}

// Close shuts down server stream gracefully.
func (p *ActorPlacement) Close() {
	// CAS to avoid stop more than once.
	if p.shutdown.CompareAndSwap(false, true) {
		close(p.closedCh)
	}
	p.wg.Wait()
}

// WaitUntilPlacementTableIsReady waits until placement table is until table lock is unlocked.
func (p *ActorPlacement) WaitUntilPlacementTableIsReady(ctx context.Context) error {
	if !p.tableIsBlocked.Load() {
		return nil
	}
	select {
	case <-p.unblockSignal:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// LookupActor resolves to actor service instance address using consistent hashing table.
func (p *ActorPlacement) LookupActor(actorType, actorID string) (string, string) {
	p.placementTableLock.RLock()
	defer p.placementTableLock.RUnlock()

	if p.placementTables == nil {
		return "", ""
	}

	t := p.placementTables.Entries[actorType]
	if t == nil {
		return "", ""
	}
	host, err := t.GetHost(actorID)
	if err != nil || host == nil {
		return "", ""
	}
	return host.Name, host.AppID
}

// onPlacementError closes the current placement stream and reestablish the connection again,
// uses a different placement server depending on the error code
func (p *ActorPlacement) onPlacementError(err error) {
	s, ok := status.FromError(err)
	// If the current server is not leader or received EOF, then it will try to
	// the next server.
	if ok && (s.Code() == codes.FailedPrecondition || s.Code() == codes.Unavailable) {
		p.serverIndex.Store((p.serverIndex.Load() + 1) % int32(len(p.serverAddr)))
	} else {
		log.Debugf("disconnected from placement: %v", err)
	}
}

func (p *ActorPlacement) onPlacementOrder(ctx context.Context, in *v1pb.PlacementOrder) {
	log.Debugf("placement order received: %s", in.Operation)
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(in.Operation)

	// lock all incoming calls when an updated table arrives
	p.operationUpdateLock.Lock()
	defer p.operationUpdateLock.Unlock()

	switch in.Operation {
	case lockOperation:
		p.blockPlacements()

		p.wg.Add(1)
		go func() {
			defer p.wg.Done()
			// TODO: Use lock-free table update.
			// current implementation is distributed two-phase locking algorithm.
			// If placement experiences intermittently outage during updateplacement,
			// user application will face 5 second blocking even if it can avoid deadlock.
			// It can impact the entire system.
			select {
			case <-p.clock.After(time.Second * 5):
			case <-ctx.Done():
			}
			p.unblockPlacements()
		}()

	case unlockOperation:
		p.unblockPlacements()

	case updateOperation:
		p.updatePlacements(in.Tables)
	}
}

func (p *ActorPlacement) blockPlacements() {
	p.unblockSignal = make(chan struct{})
	p.tableIsBlocked.Store(true)
}

func (p *ActorPlacement) unblockPlacements() {
	if p.tableIsBlocked.CompareAndSwap(true, false) {
		close(p.unblockSignal)
	}
}

func (p *ActorPlacement) updatePlacements(in *v1pb.PlacementTables) {
	updated := false
	func() {
		p.placementTableLock.Lock()
		defer p.placementTableLock.Unlock()

		if in.Version == p.placementTables.Version {
			return
		}

		tables := &hashing.ConsistentHashTables{Entries: make(map[string]*hashing.Consistent)}
		for k, v := range in.Entries {
			loadMap := map[string]*hashing.Host{}
			for lk, lv := range v.LoadMap {
				loadMap[lk] = hashing.NewHost(lv.Name, lv.Id, lv.Load, lv.Port)
			}
			tables.Entries[k] = hashing.NewFromExisting(v.Hosts, v.SortedSet, loadMap)
		}

		p.placementTables = tables
		p.placementTables.Version = in.Version
		updated = true
	}()

	if !updated {
		return
	}

	// May call LookupActor inside, so should not do this with placementTableLock locked.
	p.afterTableUpdateFn()

	log.Infof("placement tables updated, version: %s", in.GetVersion())
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
