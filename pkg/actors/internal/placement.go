// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package internal

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"go.uber.org/atomic"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/kit/logger"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/runtime/security"
)

var log = logger.NewLogger("dapr.runtime.actor.internal.placement")

const (
	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"

	placementReconnectInterval    = 500 * time.Millisecond
	statusReportHeartbeatInterval = 1 * time.Second

	grpcServiceConfig = `{"loadBalancingPolicy":"round_robin"}`
)

// ActorPlacement maintains membership of actor instances and consistent hash
// tables to discover the actor while interacting with Placement service.
type ActorPlacement struct {
	actorTypes []string
	appID      string
	// runtimeHostname is the address and port of the runtime
	runtimeHostName string

	// serverAddr is the list of placement addresses.
	serverAddr []string
	// serverIndex is the current index of placement servers in serverAddr.
	serverIndex atomic.Int32

	// clientCert is the workload certificate to connect placement.
	clientCert *dapr_credentials.CertChain

	// clientLock is the lock for client conn and stream.
	clientLock *sync.RWMutex
	// clientConn is the gRPC client connection.
	clientConn *grpc.ClientConn
	// clientStream is the client side stream.
	clientStream v1pb.Placement_ReportDaprStatusClient
	// streamConnAlive is the status of stream connection alive.
	streamConnAlive bool
	// streamConnectedCond is the condition variable for goroutines waiting for or announcing
	// that the stream between runtime and placement is connected.
	streamConnectedCond *sync.Cond

	// placementTables is the consistent hashing table map to
	// look up Dapr runtime host address to locate actor.
	placementTables *hashing.ConsistentHashTables
	// placementTableLock is the lock for placementTables.
	placementTableLock *sync.RWMutex

	// unblockSignal is the channel to unblock table locking.
	unblockSignal chan struct{}
	// tableIsBlocked is the status of table lock.
	tableIsBlocked *atomic.Bool
	// operationUpdateLock is the lock for three stage commit.
	operationUpdateLock *sync.Mutex

	// appHealthFn is the user app health check callback.
	appHealthFn func() bool
	// afterTableUpdateFn is function for post processing done after table updates,
	// such as draining actors and resetting reminders.
	afterTableUpdateFn func()

	// shutdown is the flag when runtime is being shutdown.
	shutdown atomic.Bool
	// shutdownConnLoop is the wait group to wait until all connection loop are done
	shutdownConnLoop sync.WaitGroup
}

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

// NewActorPlacement initializes ActorPlacement for the actor service.
func NewActorPlacement(
	serverAddr []string, clientCert *dapr_credentials.CertChain,
	appID, runtimeHostName string, actorTypes []string,
	appHealthFn func() bool,
	afterTableUpdateFn func()) *ActorPlacement {
	return &ActorPlacement{
		actorTypes:      actorTypes,
		appID:           appID,
		runtimeHostName: runtimeHostName,
		serverAddr:      addDNSResolverPrefix(serverAddr),

		clientCert: clientCert,

		clientLock:          &sync.RWMutex{},
		streamConnAlive:     false,
		streamConnectedCond: sync.NewCond(&sync.Mutex{}),

		placementTableLock: &sync.RWMutex{},
		placementTables:    &hashing.ConsistentHashTables{Entries: make(map[string]*hashing.Consistent)},

		operationUpdateLock: &sync.Mutex{},
		tableIsBlocked:      atomic.NewBool(false),
		appHealthFn:         appHealthFn,
		afterTableUpdateFn:  afterTableUpdateFn,
	}
}

// Start connects placement service to register to membership and send heartbeat
// to report the current member status periodically.
func (p *ActorPlacement) Start() {
	p.serverIndex.Store(0)
	p.shutdown.Store(false)

	established := false
	func() {
		p.clientLock.Lock()
		defer p.clientLock.Unlock()

		p.clientStream, p.clientConn = p.establishStreamConn()
		if p.clientStream == nil {
			return
		}
		established = true
	}()
	if !established {
		return
	}

	func() {
		p.streamConnectedCond.L.Lock()
		defer p.streamConnectedCond.L.Unlock()

		p.streamConnAlive = true
		p.streamConnectedCond.Broadcast()
	}()

	// Establish receive channel to retrieve placement table update
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		for !p.shutdown.Load() {
			p.clientLock.RLock()
			clientStream := p.clientStream
			p.clientLock.RUnlock()
			resp, err := clientStream.Recv()
			if p.shutdown.Load() {
				break
			}

			// TODO: we may need to handle specific errors later.
			if err != nil {
				p.closeStream()

				s, ok := status.FromError(err)
				// If the current server is not leader, then it will try to the next server.
				if ok && s.Code() == codes.FailedPrecondition {
					p.serverIndex.Store((p.serverIndex.Load() + 1) % int32(len(p.serverAddr)))
				} else {
					log.Debugf("disconnected from placement: %v", err)
				}

				newStream, newConn := p.establishStreamConn()
				if newStream != nil {
					p.clientLock.Lock()
					p.clientConn = newConn
					p.clientStream = newStream
					p.clientLock.Unlock()

					func() {
						p.streamConnectedCond.L.Lock()
						defer p.streamConnectedCond.L.Unlock()

						p.streamConnAlive = true
						p.streamConnectedCond.Broadcast()
					}()
				}

				continue
			}

			p.onPlacementOrder(resp)
		}
	}()

	// Send the current host status to placement to register the member and
	// maintain the status of member by placement.
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		for !p.shutdown.Load() {
			// Wait until stream is connected.
			func() {
				p.streamConnectedCond.L.Lock()
				defer p.streamConnectedCond.L.Unlock()

				for !p.streamConnAlive && !p.shutdown.Load() {
					p.streamConnectedCond.Wait()
				}
			}()

			if p.shutdown.Load() {
				break
			}

			// appHealthFn is the health status of actor service application. This allows placement to update
			// the member list and hashing table quickly.
			if !p.appHealthFn() {
				// app is unresponsive, close the stream and disconnect from the placement service.
				// Then Placement will remove this host from the member list.
				log.Debug("disconnecting from placement service by the unhealthy app.")

				p.closeStream()
				continue
			}

			host := v1pb.Host{
				Name:     p.runtimeHostName,
				Entities: p.actorTypes,
				Id:       p.appID,
				Load:     1, // Not used yet
				// Port is redundant because Name should include port number
			}

			var err error
			// Do lock to avoid being called with CloseSend concurrently
			func() {
				p.clientLock.RLock()
				defer p.clientLock.RUnlock()

				// p.clientStream is nil when the app is unhealthy and
				// daprd is disconnected from the placement service.
				if p.clientStream != nil {
					err = p.clientStream.Send(&host)
				}
			}()

			if err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
				log.Debugf("failed to report status to placement service : %v", err)
			}

			// No delay if stream connection is not alive.
			p.streamConnectedCond.L.Lock()
			streamConnAlive := p.streamConnAlive
			p.streamConnectedCond.L.Unlock()
			if streamConnAlive {
				diag.DefaultMonitoring.ActorStatusReported("send")
				time.Sleep(statusReportHeartbeatInterval)
			}
		}
	}()
}

// Stop shuts down server stream gracefully.
func (p *ActorPlacement) Stop() {
	// CAS to avoid stop more than once.
	if p.shutdown.CAS(false, true) {
		p.closeStream()
	}
	p.shutdownConnLoop.Wait()
}

func (p *ActorPlacement) closeStream() {
	func() {
		p.clientLock.Lock()
		defer p.clientLock.Unlock()

		if p.clientStream != nil {
			p.clientStream.CloseSend()
			p.clientStream = nil
		}

		if p.clientConn != nil {
			p.clientConn.Close()
			p.clientConn = nil
		}
	}()

	func() {
		p.streamConnectedCond.L.Lock()
		defer p.streamConnectedCond.L.Unlock()

		p.streamConnAlive = false
		// Let waiters wake up from block
		p.streamConnectedCond.Broadcast()
	}()
}

func (p *ActorPlacement) establishStreamConn() (v1pb.Placement_ReportDaprStatusClient, *grpc.ClientConn) {
	for !p.shutdown.Load() {
		serverAddr := p.serverAddr[p.serverIndex.Load()]

		// Stop reconnecting to placement until app is healthy.
		if !p.appHealthFn() {
			time.Sleep(placementReconnectInterval)
			continue
		}

		log.Debugf("try to connect to placement service: %s", serverAddr)

		opts, err := dapr_credentials.GetClientOptions(p.clientCert, security.TLSServerName)
		if err != nil {
			log.Errorf("failed to establish TLS credentials for actor placement service: %s", err)
			return nil, nil
		}

		if diag.DefaultGRPCMonitoring.IsEnabled() {
			opts = append(
				opts,
				grpc.WithUnaryInterceptor(diag.DefaultGRPCMonitoring.UnaryClientInterceptor()))
		}

		if len(p.serverAddr) == 1 && strings.HasPrefix(p.serverAddr[0], "dns:///") {
			// In Kubernetes environment, dapr-placement headless service resolves multiple IP addresses.
			// With round robin load balancer, Dapr can find the leader automatically.
			opts = append(opts, grpc.WithDefaultServiceConfig(grpcServiceConfig))
		}

		conn, err := grpc.Dial(serverAddr, opts...)
	NEXT_SERVER:
		if err != nil {
			log.Debugf("error connecting to placement service: %v", err)
			if conn != nil {
				conn.Close()
			}
			p.serverIndex.Store((p.serverIndex.Load() + 1) % int32(len(p.serverAddr)))
			time.Sleep(placementReconnectInterval)
			continue
		}

		client := v1pb.NewPlacementClient(conn)
		stream, err := client.ReportDaprStatus(context.Background())
		if err != nil {
			goto NEXT_SERVER
		}

		log.Debugf("established connection to placement service at %s", conn.Target())
		return stream, conn
	}

	return nil, nil
}

func (p *ActorPlacement) onPlacementOrder(in *v1pb.PlacementOrder) {
	log.Debugf("placement order received: %s", in.Operation)
	diag.DefaultMonitoring.ActorPlacementTableOperationReceived(in.Operation)

	// lock all incoming calls when an updated table arrives
	p.operationUpdateLock.Lock()
	defer p.operationUpdateLock.Unlock()

	switch in.Operation {
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
		p.updatePlacements(in.Tables)
	}
}

func (p *ActorPlacement) blockPlacements() {
	p.unblockSignal = make(chan struct{})
	p.tableIsBlocked.Store(true)
}

func (p *ActorPlacement) unblockPlacements() {
	if p.tableIsBlocked.CAS(true, false) {
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

// WaitUntilPlacementTableIsReady waits until placement table is until table lock is unlocked.
func (p *ActorPlacement) WaitUntilPlacementTableIsReady() {
	if p.tableIsBlocked.Load() {
		<-p.unblockSignal
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
