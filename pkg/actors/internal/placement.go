// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package internal

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	dapr_credentials "github.com/dapr/dapr/pkg/credentials"
	diag "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/placement/hashing"
	v1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/runtime/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	// serverIndex is the the current index of placement servers in serverAddr.
	serverIndex int
	// streamConnAlive is the status of stream connection alive.
	streamConnAlive bool
	// streamConnectedCh is the channel to notify that the stream
	// between runtime and placement is connected.
	streamConnectedCh chan struct{}
	// clientCert is the workload certificate to connect placement.
	clientCert *dapr_credentials.CertChain
	// clientConn is the gRPC client connection.
	clientConn *grpc.ClientConn
	// clientStream is the client side stream.
	clientStream v1pb.Placement_ReportDaprStatusClient

	// placementTables is the consistent hashing table map to
	// look up Dapr runtime host address to locate actor.
	placementTables *hashing.ConsistentHashTables
	// placementTableLock is the lock for placementTables.
	placementTableLock *sync.RWMutex

	// unblockSignal is the channel to unblock table locking.
	unblockSignal chan struct{}
	// tableIsBlocked is the status of table lock.
	tableIsBlocked bool
	// operationUpdateLock is the lock for three stage commit.
	operationUpdateLock *sync.Mutex

	// appHealthFn is the user app health check callback.
	appHealthFn func() bool
	// afterTableUpdateFn is function for post processing done after table updates,
	// such as draining actors and resetting reminders.
	afterTableUpdateFn func()

	// shutdown is the flag when runtime is being shutdown.
	shutdown bool
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
		serverIndex:     0,

		placementTableLock:  &sync.RWMutex{},
		placementTables:     &hashing.ConsistentHashTables{Entries: make(map[string]*hashing.Consistent)},
		clientCert:          clientCert,
		operationUpdateLock: &sync.Mutex{},

		appHealthFn:        appHealthFn,
		afterTableUpdateFn: afterTableUpdateFn,

		shutdown: false,
	}
}

// Start connects placement service to register to membership and send heartbeat
// to report the current member status periodically.
func (p *ActorPlacement) Start() {
	// streamConnAlive represents the status of stream channel. This must be changed in receiver loop.
	// This flag reduces the unnecessary request retry.
	p.streamConnAlive = true
	p.streamConnectedCh = make(chan struct{})
	p.serverIndex = 0
	p.shutdown = false
	p.clientStream, p.clientConn = p.establishStreamConn()
	if p.clientStream == nil {
		return
	}

	// Establish receive channel to retrieve placement table update
	p.shutdownConnLoop.Add(1)
	go func() {
		defer p.shutdownConnLoop.Done()
		for !p.shutdown {
			resp, err := p.clientStream.Recv()
			if p.shutdown {
				break
			}

			// TODO: we may need to handle specific errors later.
			if !p.streamConnAlive || err != nil {
				p.streamConnAlive = false
				p.closeStream()

				s, ok := status.FromError(err)
				// If the current server is not leader, then it will try to the next server.
				if ok && s.Code() == codes.FailedPrecondition {
					p.serverIndex = (p.serverIndex + 1) % len(p.serverAddr)
				} else {
					log.Debugf("disconnected from placement: %v", err)
				}

				newStream, newConn := p.establishStreamConn()
				if newStream != nil {
					p.clientConn = newConn
					p.clientStream = newStream
					p.streamConnAlive = true
					close(p.streamConnectedCh)
					p.streamConnectedCh = make(chan struct{})
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
		for !p.shutdown {
			// Wait until stream is reconnected.
			if !p.streamConnAlive {
				<-p.streamConnectedCh
			}

			if p.shutdown {
				break
			}

			// appHealthFn is the health status of actor service application. This allows placement to update
			// memberlist and hashing table quickly.
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

			err := p.clientStream.Send(&host)
			if err != nil {
				diag.DefaultMonitoring.ActorStatusReportFailed("send", "status")
				log.Debugf("failed to report status to placement service : %v", err)
			}

			// No delay if stream connection is not alive.
			if p.streamConnAlive {
				diag.DefaultMonitoring.ActorStatusReported("send")
				time.Sleep(statusReportHeartbeatInterval)
			}
		}
	}()
}

// Stop shuts down server stream gracefully.
func (p *ActorPlacement) Stop() {
	if p.shutdown {
		return
	}

	p.shutdown = true
	if p.streamConnectedCh != nil {
		close(p.streamConnectedCh)
	}
	p.closeStream()
	p.shutdownConnLoop.Wait()
}

func (p *ActorPlacement) closeStream() {
	if p.clientStream != nil {
		p.clientStream.CloseSend()
	}

	if p.clientConn != nil {
		p.clientConn.Close()
	}
}

func (p *ActorPlacement) establishStreamConn() (v1pb.Placement_ReportDaprStatusClient, *grpc.ClientConn) {
	for !p.shutdown {
		serverAddr := p.serverAddr[p.serverIndex]

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
			p.serverIndex = (p.serverIndex + 1) % len(p.serverAddr)
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

	p.afterTableUpdateFn()

	p.placementTableLock.Unlock()

	log.Infof("placement tables updated, version: %s", in.GetVersion())
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
