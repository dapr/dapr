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
	"github.com/dapr/dapr/pkg/placement"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/runtime/security"
	"google.golang.org/grpc"
)

const (
	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"

	placementReconnectInterval    = 500 * time.Millisecond
	statusReportHeartbeatInterval = 1 * time.Second
)

var log = logger.NewLogger("dapr.runtime.actor.internal.membership")

// ActorMembership maintains membership of actor instances and consistent hash
// tables to discover the actor while interacting with Placement service.
type ActorMembership struct {
	actorTypes []string
	appID      string
	hostname   string

	placementAddress   string
	placementTables    *placement.ConsistentHashTables
	placementTableLock *sync.RWMutex
	placementConnAlive bool

	placementSignal     chan struct{}
	placementBlock      bool
	placementClientCert *dapr_credentials.CertChain

	operationUpdateLock *sync.Mutex

	serviceHealthFn     func() bool
	drainActorsFn       func()
	evaluateRemindersFn func()
}

// NewActorMembership initializes ActorMembership for the actor service.
func NewActorMembership(
	placementAddress string, clientCert *dapr_credentials.CertChain,
	appID, hostname string, actorTypes []string,
	serviceHealthFn func() bool,
	drainActorFn func(),
	evaluateRemindersFn func()) *ActorMembership {
	return &ActorMembership{
		actorTypes:       actorTypes,
		appID:            appID,
		hostname:         hostname,
		placementAddress: placementAddress,

		placementTableLock:  &sync.RWMutex{},
		placementTables:     &placement.ConsistentHashTables{Entries: make(map[string]*placement.Consistent)},
		placementClientCert: clientCert,
		operationUpdateLock: &sync.Mutex{},

		serviceHealthFn:     serviceHealthFn,
		drainActorsFn:       drainActorFn,
		evaluateRemindersFn: evaluateRemindersFn,
	}
}

// Start connects placement service to register to membership and send heartbeat
// to report the current member status periodically.
func (am *ActorMembership) Start() {
	// placementConnAlive represents the status of stream channel. This must be changed in receiver loop.
	// This flag reduces the unnecessary request retry.
	am.placementConnAlive = true
	stream := am.newPlacementStreamConn(am.placementAddress)

	// Establish receive channel to retrieve placement table update
	go func() {
		for {
			// Wait until stream is reconnected.
			if !am.placementConnAlive || stream == nil {
				time.Sleep(placementReconnectInterval)
				continue
			}

			resp, err := stream.Recv()
			if err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("recv", "status")
				log.Warnf("failed to receive placement table update from placement service: %v", err)

				// If receive channel is down, ensure the current stream is closed and get new gRPC stream.
				stream.CloseSend()
				am.placementConnAlive = false
				stream = am.newPlacementStreamConn(am.placementAddress)
				am.placementConnAlive = true

				continue
			}

			diag.DefaultMonitoring.ActorStatusReported("recv")
			am.onPlacementOrder(resp)
		}
	}()

	// Send the current host status to placement to register the member and
	// maintain the status of member by placement.
	go func() {
		for {
			// Wait until stream is reconnected.
			if !am.placementConnAlive || stream == nil {
				time.Sleep(placementReconnectInterval)
				continue
			}

			// serviceHealthFn() is the health status of actor service application. This allows placement to update
			// memberlist and hashing table quickly.
			if !am.serviceHealthFn() {
				// app is unresponsive, close the stream and disconnect from the placement service.
				// Then Placement will remove this host from the member list.
				err := stream.CloseSend()
				if err != nil {
					log.Errorf("error closing stream to placement service: %s", err)
				}
				continue
			}

			host := placementv1pb.Host{
				Name:     am.hostname,
				Load:     1, // TODO: Use CPU/Memory as load balancing factor
				Entities: am.actorTypes,
				Port:     0, // TODO: Remove Port later
				Id:       am.appID,
			}

			if err := stream.Send(&host); err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
				log.Warnf("failed to report status to placement service : %v", err)
			}

			time.Sleep(statusReportHeartbeatInterval)
		}
	}()
}

func (am *ActorMembership) newPlacementStreamConn(placementAddress string) placementv1pb.Placement_ReportDaprStatusClient {
	log.Infof("starting connection attempt to placement service: %s", placementAddress)

	for {
		opts, err := dapr_credentials.GetClientOptions(am.placementClientCert, security.TLSServerName)
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

		conn, err := grpc.Dial(placementAddress, opts...)
		if err != nil {
			log.Warnf("error connecting to placement service: %v", err)
			diag.DefaultMonitoring.ActorStatusReportFailed("dial", "placement")
			conn.Close()
			time.Sleep(placementReconnectInterval)
			continue
		}

		client := placementv1pb.NewPlacementClient(conn)
		stream, err := client.ReportDaprStatus(context.Background())
		if err != nil {
			log.Warnf("error establishing client to placement service: %v", err)
			diag.DefaultMonitoring.ActorStatusReportFailed("establish", "status")
			conn.Close()
			time.Sleep(placementReconnectInterval)
			continue
		}

		log.Infof("established connection to placement service at %s", placementAddress)
		return stream
	}
}

func (am *ActorMembership) onPlacementOrder(in *placementv1pb.PlacementOrder) {
	log.Infof("placement order received: %s", in.Operation)
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(in.Operation)

	// lock all incoming calls when an updated table arrives
	am.operationUpdateLock.Lock()
	defer am.operationUpdateLock.Unlock()

	switch in.Operation {
	case lockOperation:
		am.blockPlacements()

		go func() {
			// TODO: Use lock-free table update.
			// current implemenation is distributed two-phase locking algorithm.
			// If placement experiences intermitently outage during updateplacement,
			// user application will face 5 second blocking even if it can avoid deadlock.
			time.Sleep(time.Second * 5)
			am.unblockPlacements()
		}()

	case unlockOperation:
		am.unblockPlacements()

	case updateOperation:
		am.updatePlacements(in.Tables)
	}
}

func (am *ActorMembership) blockPlacements() {
	am.placementSignal = make(chan struct{})
	am.placementBlock = true
}

func (am *ActorMembership) unblockPlacements() {
	if am.placementBlock {
		am.placementBlock = false
		close(am.placementSignal)
	}
}

func (am *ActorMembership) updatePlacements(in *placementv1pb.PlacementTables) {
	if in.Version == am.placementTables.Version {
		return
	}

	am.placementTableLock.Lock()

	for k, v := range in.Entries {
		loadMap := map[string]*placement.Host{}
		for lk, lv := range v.LoadMap {
			loadMap[lk] = placement.NewHost(lv.Name, lv.Id, lv.Load, lv.Port)
		}
		am.placementTables.Entries[k] = placement.NewFromExisting(v.Hosts, v.SortedSet, loadMap)
	}

	am.placementTables.Version = in.Version

	am.drainActorsFn()
	log.Infof("placement tables updated, version: %s", in.GetVersion())
	am.evaluateRemindersFn()

	am.placementTableLock.Unlock()
}

// WaitUntilPlacementTableIsReady waits until placement table is until table lock is unlocked.
func (am *ActorMembership) WaitUntilPlacementTableIsReady() {
	if am.placementBlock {
		<-am.placementSignal
	}
}

// LookupActorAddress resolves to actor service instance address using consistent hashing table.
func (am *ActorMembership) LookupActorAddress(actorType, actorID string) (string, string) {
	if am.placementTables == nil {
		return "", ""
	}

	t := am.placementTables.Entries[actorType]
	if t == nil {
		return "", ""
	}
	host, err := t.GetHost(actorID)
	if err != nil || host == nil {
		return "", ""
	}
	return host.Name, host.AppID
}
