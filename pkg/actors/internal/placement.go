// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package internal

import (
	"context"
	"sync"
	"time"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/runtime/security"
	"google.golang.org/grpc"
)

var log = logger.NewLogger("dapr.runtime.actor.internal.placement")

const (
	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"

	placementReconnectInterval    = 500 * time.Millisecond
	statusReportHeartbeatInterval = 1 * time.Second
)

// ActorPlacement maintains membership of actor instances and consistent hash
// tables to discover the actor while interacting with Placement service.
type ActorPlacement struct {
	actorTypes      []string
	appID           string
	runtimeHostName string

	serverAddr      []string
	serverConnAlive bool
	clientCert      *dapr_credentials.CertChain

	placementTables    *hashing.ConsistentHashTables
	placementTableLock *sync.RWMutex

	unblockSignal       chan struct{}
	tableIsBlocked      bool
	operationUpdateLock *sync.Mutex

	appHealthFn         func() bool
	drainActorsFn       func()
	evaluateRemindersFn func()
}

// NewActorPlacement initializes ActorPlacement for the actor service.
func NewActorPlacement(
	serverAddr []string, clientCert *dapr_credentials.CertChain,
	appID, runtimeHostName string, actorTypes []string,
	appHealthFn func() bool,
	drainActorFn func(),
	evaluateRemindersFn func()) *ActorPlacement {
	return &ActorPlacement{
		actorTypes:      actorTypes,
		appID:           appID,
		runtimeHostName: runtimeHostName,
		serverAddr:      serverAddr,

		placementTableLock:  &sync.RWMutex{},
		placementTables:     &hashing.ConsistentHashTables{Entries: make(map[string]*hashing.Consistent)},
		clientCert:          clientCert,
		operationUpdateLock: &sync.Mutex{},

		appHealthFn:         appHealthFn,
		drainActorsFn:       drainActorFn,
		evaluateRemindersFn: evaluateRemindersFn,
	}
}

// Start connects placement service to register to membership and send heartbeat
// to report the current member status periodically.
func (p *ActorPlacement) Start() {
	// serverConnAlive represents the status of stream channel. This must be changed in receiver loop.
	// This flag reduces the unnecessary request retry.
	p.serverConnAlive = true
	stream := p.newPlacementStreamConn(p.serverAddr)

	// Establish receive channel to retrieve placement table update
	go func() {
		for {
			// Wait until stream is reconnected.
			if !p.serverConnAlive || stream == nil {
				time.Sleep(placementReconnectInterval)
				continue
			}

			resp, err := stream.Recv()
			if err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("recv", "status")
				log.Warnf("failed to receive placement table update from placement service: %v", err)
				time.Sleep(statusReportHeartbeatInterval)
				continue
			}

			diag.DefaultMonitoring.ActorStatusReported("recv")
			p.onPlacementOrder(resp)
		}
	}()

	// Send the current host status to placement to register the member and
	// maintain the status of member by placement.
	go func() {
		for {
			// Wait until stream is reconnected.
			if !p.serverConnAlive || stream == nil {
				time.Sleep(placementReconnectInterval)
				continue
			}

			host := v1pb.Host{
				Name:     p.runtimeHostName,
				Entities: p.actorTypes,
				Id:       p.appID,
				Load:     1, // Not used yet
				// Port is redundant because Name should include port number
			}

			if err := stream.Send(&host); err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
				log.Warnf("failed to report status to placement service : %v", err)

				// If receive channel is down, ensure the current stream is closed and get new gRPC strep.
				stream.CloseSend()
				p.serverConnAlive = false
				stream = p.newPlacementStreamConn(p.serverAddr)
				p.serverConnAlive = true
			}

			// appHealthFn is the health status of actor service application. This allows placement to update
			// memberlist and hashing table quickly.
			if !p.appHealthFn() {
				// app is unresponsive, close the stream and disconnect from the placement service.
				// Then Placement will remove this host from the member list.
				err := stream.CloseSend()
				if err != nil {
					log.Errorf("error closing stream to placement service: %s", err)
				}
				continue
			}

			time.Sleep(statusReportHeartbeatInterval)
		}
	}()
}

func (p *ActorPlacement) newPlacementStreamConn(serverAddr []string) v1pb.Placement_ReportDaprStatusClient {
	log.Infof("starting connection attempt to placement service: %s", serverAddr)

	nodeIndex := 0

	for {
		// Stop reconnecting to placement until app is healthy.
		if !p.appHealthFn() {
			time.Sleep(placementReconnectInterval)
			continue
		}

		opts, err := dapr_credentials.GetClientOptions(p.clientCert, security.TLSServerName)
		if err != nil {
			log.Errorf("failed to establish TLS credentials for actor placement service: %s", err)
			return nil
		}
		opts = append(opts, grpc.WithBlock())
		if diag.DefaultGRPCMonitoring.IsEnabled() {
			opts = append(
				opts,
				grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()))
		}

		conn, err := grpc.Dial(serverAddr[nodeIndex], opts...)
		if err != nil {
			log.Warnf("error connecting to placement service: %v", err)
			diag.DefaultMonitoring.ActorStatusReportFailed("dial", "placement")
			conn.Close()
			nodeIndex = (nodeIndex + 1) % len(serverAddr)
			time.Sleep(placementReconnectInterval)
			continue
		}

		client := v1pb.NewPlacementClient(conn)
		stream, err := client.ReportDaprStatus(context.Background())
		if err != nil {
			log.Warnf("error establishing client to placement service: %v", err)
			diag.DefaultMonitoring.ActorStatusReportFailed("establish", "status")
			conn.Close()
			nodeIndex = (nodeIndex + 1) % len(serverAddr)
			time.Sleep(placementReconnectInterval)
			continue
		}

		log.Infof("established connection to placement service at %s", serverAddr)
		return stream
	}
}

func (p *ActorPlacement) onPlacementOrder(in *v1pb.PlacementOrder) {
	log.Infof("placement order received: %s", in.Operation)
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(in.Operation)

	// lock all incoming calls when an updated table arrives
	p.operationUpdateLock.Lock()
	defer p.operationUpdateLock.Unlock()

	switch in.Operation {
	case lockOperation:
		p.blockPlacements()

		go func() {
			// TODO: Use lock-free table update.
			// current implemenation is distributed two-phase locking algorithm.
			// If placement experiences intermitently outage during updateplacement,
			// user application will face 5 second blocking even if it can avoid deadlock.
			// It can impact the entire system.
			time.Sleep(time.Second * 5)
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
	p.tableIsBlocked = true
}

func (p *ActorPlacement) unblockPlacements() {
	if p.tableIsBlocked {
		p.tableIsBlocked = false
		close(p.unblockSignal)
	}
}

func (p *ActorPlacement) updatePlacements(in *v1pb.PlacementTables) {
	if in.Version == p.placementTables.Version {
		return
	}

	p.placementTableLock.Lock()

	for k, v := range in.Entries {
		loadMap := map[string]*hashing.Host{}
		for lk, lv := range v.LoadMap {
			loadMap[lk] = hashing.NewHost(lv.Name, lv.Id, lv.Load, lv.Port)
		}
		p.placementTables.Entries[k] = hashing.NewFromExisting(v.Hosts, v.SortedSet, loadMap)
	}

	p.placementTables.Version = in.Version

	p.drainActorsFn()
	log.Infof("placement tables updated, version: %s", in.GetVersion())
	p.evaluateRemindersFn()

	p.placementTableLock.Unlock()
}

// WaitUntilPlacementTableIsReady waits until placement table is until table lock is unlocked.
func (p *ActorPlacement) WaitUntilPlacementTableIsReady() {
	if p.tableIsBlocked {
		<-p.unblockSignal
	}
}

// LookupActor resolves to actor service instance address using consistent hashing table.
func (p *ActorPlacement) LookupActor(actorType, actorID string) (string, string) {
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
