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
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement")

const (
	// membershipChangeChSize is the channel size of membership change request from Dapr runtime.
	// MembershipChangeWorker will process actor host member change request.
	membershipChangeChSize = 100

	// faultyHostDetectDuration is the maximum duration when existing host is marked as faulty.
	// Dapr runtime sends heartbeat every 1 second. Whenever placement server gets the heartbeat,
	// it updates the last heartbeat time in UpdateAt of the FSM state. If Now - UpdatedAt exceeds
	// faultyHostDetectDuration, membershipChangeWorker() tries to remove faulty Dapr runtim/ from
	// membership.
	// When placement gets the leadership, faultyHostDetectionDuration will be faultyHostDetectInitialDuration.
	// This duration will give more time to let each runtime find the leader of placement nodes.
	// Once the first dissemination happens after getting leadership, membershipChangeWorker will
	// use faultyHostDetectDefaultDuration.
	faultyHostDetectInitialDuration = 6 * time.Second
	faultyHostDetectDefaultDuration = 3 * time.Second

	// faultyHostDetectInterval is the interval to check the faulty member.
	faultyHostDetectInterval = 500 * time.Millisecond

	// disseminateTimerInterval is the interval to disseminate the latest consistent hashing table.
	disseminateTimerInterval = 500 * time.Millisecond
	// disseminateTimeout is the timeout to disseminate hashing tables after the membership change.
	// When the multiple actor service pods are deployed first, a few pods are deployed in the beginning
	// and the rest of pods will be deployed gradually. disseminateNextTime is maintained to decide when
	// the hashing table is disseminated. disseminateNextTime is updated whenever membership change
	// is applied to raft state or each pod is deployed. If we increase disseminateTimeout, it will
	// reduce the frequency of dissemination, but it will delay the table dissemination.
	disseminateTimeout = 2 * time.Second
)

type hostMemberChange struct {
	cmdType raft.CommandType
	host    raft.DaprHostMember
}

type tablesUpdateRequest struct {
	hosts            []daprdStream
	tables           *placementv1pb.PlacementTables
	tablesWithVNodes *placementv1pb.PlacementTables // Temporary. Will be removed in 1.15
}

// GetVersion is used only for logs in membership.go
func (r *tablesUpdateRequest) GetVersion() string {
	if r.tables != nil {
		return r.tables.GetVersion()
	}
	if r.tablesWithVNodes != nil {
		return r.tablesWithVNodes.GetVersion()
	}

	return ""
}

func (r *tablesUpdateRequest) SetAPILevel(minAPILevel uint32, maxAPILevel *uint32) {
	setAPILevel := func(tables *placementv1pb.PlacementTables) {
		if tables != nil {
			if tables.GetApiLevel() < minAPILevel {
				tables.ApiLevel = minAPILevel
			}
			if maxAPILevel != nil && tables.GetApiLevel() > *maxAPILevel {
				tables.ApiLevel = *maxAPILevel
			}
		}
	}

	setAPILevel(r.tablesWithVNodes)
	setAPILevel(r.tables)
}

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	streamConnPool *streamConnPool

	// raftNode is the raft server instance.
	raftNode *raft.Server

	// lastHeartBeat represents the last time stamp when runtime sent heartbeat.
	lastHeartBeat sync.Map
	// membershipCh is the channel to maintain Dapr runtime host membership update.
	membershipCh chan hostMemberChange

	// disseminateLocks is a map of lock per namespace for disseminating the hashing tables
	disseminateLocks *concurrency.MutexMap

	// disseminateNextTime is the time when the hashing tables for a namespace are disseminated.
	disseminateNextTime *concurrency.AtomicMap[string, int64]

	// memberUpdateCount represents how many dapr runtimes needs to change in a namespace.
	// Only actor runtime's heartbeat will increase this.
	memberUpdateCount *concurrency.AtomicMap[string, uint32]

	// Maximum API level to return.
	// If nil, there's no limit.
	maxAPILevel *uint32
	// Minimum API level to return
	minAPILevel uint32

	// faultyHostDetectDuration
	faultyHostDetectDuration *atomic.Int64

	// hasLeadership indicates the state for leadership.
	hasLeadership atomic.Bool

	// streamConnGroup represents the number of stream connections.
	// This waits until all stream connections are drained when revoking leadership.
	streamConnGroup sync.WaitGroup

	// clock keeps time. Mocked in tests.
	clock clock.WithTicker

	sec security.Provider

	running  atomic.Bool
	closed   atomic.Bool
	closedCh chan struct{}
	wg       sync.WaitGroup
}

// PlacementServiceOpts contains options for the NewPlacementService method.
type PlacementServiceOpts struct {
	RaftNode    *raft.Server
	MaxAPILevel *uint32
	MinAPILevel uint32
	SecProvider security.Provider
}

// NewPlacementService returns a new placement service.
func NewPlacementService(opts PlacementServiceOpts) *Service {
	fhdd := &atomic.Int64{}
	fhdd.Store(int64(faultyHostDetectInitialDuration))

	return &Service{
		streamConnPool:           newStreamConnPool(),
		membershipCh:             make(chan hostMemberChange, membershipChangeChSize),
		faultyHostDetectDuration: fhdd,
		raftNode:                 opts.RaftNode,
		maxAPILevel:              opts.MaxAPILevel,
		minAPILevel:              opts.MinAPILevel,
		clock:                    &clock.RealClock{},
		closedCh:                 make(chan struct{}),
		sec:                      opts.SecProvider,
		disseminateLocks:         concurrency.NewMutexMap(),
		memberUpdateCount:        concurrency.NewAtomicMapStringUint32(),
		disseminateNextTime:      concurrency.NewAtomicMapStringInt64(),
	}

}

// Run starts the placement service gRPC server.
// Blocks until the service is closed and all connections are drained.
func (p *Service) Run(ctx context.Context, listenAddress, port string) error {
	if p.closed.Load() {
		return errors.New("placement service is closed")
	}

	if !p.running.CompareAndSwap(false, true) {
		return errors.New("placement service is already running")
	}

	sec, err := p.sec.Handler(ctx)
	if err != nil {
		return err
	}

	serverListener, err := net.Listen("tcp", fmt.Sprintf("%s:%s", listenAddress, port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	//grpcServer := grpc.NewServer(sec.GRPCServerOptionMTLS())
	grpcServer := grpc.NewServer(
		sec.GRPCServerOptionMTLS(),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    5 * time.Second, // Server pings the client if it hasn't received any requests for 5 seconds
			Timeout: 3 * time.Second, // If a client has not responded to 3 pings, it is considered disconnected
		}),
		grpc.StreamInterceptor(serverStreamInterceptor),
	)

	placementv1pb.RegisterPlacementServer(grpcServer, p)

	log.Infof("Placement service started on port %d", serverListener.Addr().(*net.TCPAddr).Port)

	errCh := make(chan error)
	go func() {
		errCh <- grpcServer.Serve(serverListener)
		log.Info("Placement service stopped")
	}()

	<-ctx.Done()

	if p.closed.CompareAndSwap(false, true) {
		close(p.closedCh)
	}

	grpcServer.GracefulStop()
	p.wg.Wait()

	return <-errCh
}

// serverStreamInterceptor is a WIP. It is used to detect when a client disconnects from a stream.
func serverStreamInterceptor(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
	err := handler(srv, ss)
	if err != nil {
		// Perform action upon client disconnection here
		fmt.Printf("\n\n\n\n-----------------------\nClient disconnected: %v\n", err)
		fmt.Printf("\nStream: %v\n", ss)
		fmt.Printf("\nStreamServerInfo: %v\n", info)
	}
	return err
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts.
func (p *Service) ReportDaprStatus(stream placementv1pb.Placement_ReportDaprStatusServer) error { //nolint:nosnakecase
	registeredMemberID := ""
	isActorRuntime := false

	clientID, err := p.validateClient(stream)
	if err != nil {
		return err
	}

	firstMessage, err := p.receiveAndValidateFirstMessage(stream, clientID)
	if err != nil {
		return err
	}

	// Older versions won't be sending the namespace in subsequent messages either,
	// so we'll save this one in a separate variable
	namespace := firstMessage.GetNamespace()

	daprStream := newDaprdStream(firstMessage, stream)
	p.streamConnGroup.Add(1)
	p.streamConnPool.add(daprStream)

	defer func() {
		p.streamConnGroup.Done()
		p.streamConnPool.delete(daprStream)
		// TODO: @elena If this is the last stream in the namespace, we need to delete elements
		// from service.memberUpdateCount and service.disseminateNextTime
	}()

	for p.hasLeadership.Load() {
		var req *placementv1pb.Host
		if firstMessage != nil {
			req, err = firstMessage, nil
			firstMessage = nil
		} else {
			req, err = stream.Recv()
		}

		switch err {
		case nil:
			if clientID != nil && req.GetId() != clientID.AppID() {
				return status.Errorf(codes.PermissionDenied, "client ID %s is not allowed", req.GetId())
			}

			if registeredMemberID == "" {
				registeredMemberID, err = p.handleNewConnection(req, daprStream, namespace)
				if err != nil {
					return err
				}
			}

			// Ensure that the incoming runtime is actor instance.
			//isActorRuntime = len(req.GetEntities()) > 0
			if len(req.GetEntities()) == 0 {
				// we already disseminated the existing tables to this member,
				// so we can ignore the rest if it's a non-actor.
				continue
			}

			now := p.clock.Now()

			for _, entity := range req.GetEntities() {
				monitoring.RecordActorHeartbeat(req.GetId(), entity, req.GetName(), req.GetPod(), now)
			}

			// Record the heartbeat timestamp. This timestamp will be used to check if the member
			// state maintained by raft is valid or not. If the member is outdated based the timestamp
			// the member will be marked as faulty node and removed.
			p.lastHeartBeat.Store(req.GetNamespace()+"-"+req.GetName(), now.UnixNano())

			// Upsert incoming member only if the existing member info
			// doesn't match with the incoming member info.
			upsertRequired := true
			state := p.raftNode.FSM().State()
			state.Lock.RLock()
			members, err := state.Members(namespace)
			if err == nil {
				if m, ok := members[req.GetName()]; ok {
					if m.AppID == req.GetId() && m.Name == req.GetName() && cmp.Equal(m.Entities, req.GetEntities()) {
						upsertRequired = false
					}
				}
			}
			state.Lock.RUnlock()

			if upsertRequired {
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberUpsert,
					host: raft.DaprHostMember{
						Name:      req.GetName(),
						AppID:     req.GetId(),
						Namespace: namespace,
						Entities:  req.GetEntities(),
						UpdatedAt: now.UnixNano(),
						APILevel:  req.GetApiLevel(),
					},
				}
				log.Debugf("Member changed; upserting appid %s in namespace %s with entities %v", req.GetId(), namespace, req.GetEntities())
			}

		default:
			if registeredMemberID == "" {
				log.Error("Stream is disconnected before member is added ", err)
				return nil
			}

			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				log.Debugf("Stream connection is disconnected gracefully: %s", registeredMemberID)
				if isActorRuntime {
					p.membershipCh <- hostMemberChange{
						cmdType: raft.MemberRemove,
						host:    raft.DaprHostMember{Name: registeredMemberID, Namespace: namespace},
					}
				}
			} else {
				// no actions for hashing table. Instead, MembershipChangeWorker will check
				// host updatedAt and if now - updatedAt > p.faultyHostDetectDuration, remove hosts.
				log.Debugf("Stream connection is disconnected with the error: %v", err)
			}

			return nil
		}
	}

	return status.Error(codes.FailedPrecondition, "only leader can serve the request")
}

func (p *Service) validateClient(stream placementv1pb.Placement_ReportDaprStatusServer) (*spiffe.Parsed, error) {
	sec, err := p.sec.Handler(stream.Context())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "")
	}

	if !sec.MTLSEnabled() {
		return nil, nil
	}

	clientID, ok, err := spiffe.FromGRPCContext(stream.Context())
	if err != nil || !ok {
		log.Debugf("failed to get client ID from context: err=%v, ok=%t", err, ok)
		return nil, status.Errorf(codes.Unauthenticated, "failed to get client ID from context")
	}

	return clientID, nil
}

func (p *Service) receiveAndValidateFirstMessage(stream placementv1pb.Placement_ReportDaprStatusServer, clientID *spiffe.Parsed) (*placementv1pb.Host, error) {
	firstMessage, err := stream.Recv()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to receive the first message: %v", err)
	}

	if clientID != nil && firstMessage.GetId() != clientID.AppID() {
		return nil, status.Errorf(codes.PermissionDenied, "client ID %s is not allowed", firstMessage.GetId())
	}

	// For older versions that are not sending their namespace as part of the message
	// we will use the namespace from the Spiffe clientID
	if clientID != nil && firstMessage.GetNamespace() == "" {
		firstMessage.Namespace = clientID.Namespace()
	}

	if clientID != nil && firstMessage.GetNamespace() != clientID.Namespace() {
		return nil, status.Errorf(codes.PermissionDenied, "client namespace %s is not allowed", firstMessage.GetNamespace())
	}

	return firstMessage, nil
}

func (p *Service) handleNewConnection(req *placementv1pb.Host, daprStream *daprdStream, namespace string) (string, error) {
	err := p.checkAPILevel(req)
	if err != nil {
		return "", err
	}

	registeredMemberID := req.GetName()

	// If the member is not an actor runtime (len(req.GetEntities()) == 0) we disseminate the tables
	// If it is actor, we'll disseminate later, after we update the member in the raft state
	if len(req.GetEntities()) == 0 {
		updateReq := &tablesUpdateRequest{
			hosts: []daprdStream{*daprStream},
		}
		if daprStream.needsVNodes {
			updateReq.tablesWithVNodes = p.raftNode.FSM().PlacementState(false, namespace)
		} else {
			updateReq.tables = p.raftNode.FSM().PlacementState(true, namespace)
		}
		err = p.performTablesUpdate(context.Background(), updateReq)
		if err != nil {
			return registeredMemberID, err
		}
	}

	return registeredMemberID, nil
}

func (p *Service) checkAPILevel(req *placementv1pb.Host) error {
	state := p.raftNode.FSM().State()
	state.Lock.RLock()
	clusterAPILevel := max(p.minAPILevel, state.APILevel())
	state.Lock.RUnlock()

	if p.maxAPILevel != nil && clusterAPILevel > *p.maxAPILevel {
		clusterAPILevel = *p.maxAPILevel
	}

	// Ensure that the reported API level is at least equal to the current one in the cluster
	if req.GetApiLevel() < clusterAPILevel {
		return status.Errorf(codes.FailedPrecondition, "The cluster's Actor API level is %d, which is higher than the reported API level %d", clusterAPILevel, req.GetApiLevel())
	}

	return nil
}
