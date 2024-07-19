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

	"github.com/alphadose/haxmap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/healthz"
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

	// disseminateTimerInterval is the interval to disseminate the latest consistent hashing table.
	disseminateTimerInterval = 500 * time.Millisecond
	// disseminateTimeout is the timeout to disseminate hashing tables after the membership change.
	// When the multiple actor service pods are deployed first, a few pods are deployed in the beginning
	// and the rest of pods will be deployed gradually. disseminateNextTime is maintained to decide when
	// the hashing table is disseminated. disseminateNextTime is updated whenever membership change
	// is applied to raft state or each pod is deployed. If we increase disseminateTimeout, it will
	// reduce the frequency of dissemination, but it will delay the table dissemination.
	disseminateTimeout = 2 * time.Second

	// faultyHostDetectDuration is the maximum duration after which a host is considered faulty.
	// Dapr runtime sends a heartbeat (stored in lastHeartBeat) every second.
	// When placement failover occurs, the new leader will wait for faultyHostDetectDuration, then
	// it will loop through all the members in the state and remove the ones that
	// have not sent a heartbeat
	faultyHostDetectDuration = 6 * time.Second

	lockOperation   = "lock"
	unlockOperation = "unlock"
	updateOperation = "update"
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
	disseminateLocks concurrency.MutexMap[string]

	// disseminateNextTime is the time when the hashing tables for a namespace are disseminated.
	disseminateNextTime haxmap.Map[string, *atomic.Int64]

	// memberUpdateCount represents how many dapr runtimes needs to change in a namespace.
	// Only actor runtime's heartbeat can increase this.
	memberUpdateCount haxmap.Map[string, *atomic.Uint32]

	// Maximum API level to return.
	// If nil, there's no limit.
	maxAPILevel *uint32
	// Minimum API level to return
	minAPILevel uint32

	// hasLeadership indicates the state for leadership.
	hasLeadership atomic.Bool

	// streamConnGroup represents the number of stream connections.
	// This waits until all stream connections are drained when revoking leadership.
	streamConnGroup sync.WaitGroup

	// clock keeps time. Mocked in tests.
	clock clock.WithTicker

	sec           security.Provider
	port          int
	listenAddress string

	htarget  healthz.Target
	running  atomic.Bool
	closed   atomic.Bool
	closedCh chan struct{}
	wg       sync.WaitGroup
}

// PlacementServiceOpts contains options for the NewPlacementService method.
type PlacementServiceOpts struct {
	RaftNode      *raft.Server
	MaxAPILevel   *uint32
	MinAPILevel   uint32
	SecProvider   security.Provider
	Port          int
	ListenAddress string
	Healthz       healthz.Healthz
}

// NewPlacementService returns a new placement service.
func NewPlacementService(opts PlacementServiceOpts) *Service {
	return &Service{
		streamConnPool:      newStreamConnPool(),
		membershipCh:        make(chan hostMemberChange, membershipChangeChSize),
		raftNode:            opts.RaftNode,
		maxAPILevel:         opts.MaxAPILevel,
		minAPILevel:         opts.MinAPILevel,
		clock:               &clock.RealClock{},
		closedCh:            make(chan struct{}),
		sec:                 opts.SecProvider,
		disseminateLocks:    concurrency.NewMutexMap[string](),
		memberUpdateCount:   *haxmap.New[string, *atomic.Uint32](),
		disseminateNextTime: *haxmap.New[string, *atomic.Int64](),
		port:                opts.Port,
		listenAddress:       opts.ListenAddress,
		htarget:             opts.Healthz.AddTarget(),
	}
}

// Start starts the placement service gRPC server.
// Blocks until the service is closed and all connections are drained.
func (p *Service) Start(ctx context.Context) error {
	defer p.htarget.NotReady()

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

	serverListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", p.listenAddress, p.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	keepaliveParams := keepalive.ServerParameters{
		Time:    2 * time.Second,
		Timeout: 3 * time.Second,
	}
	grpcServer := grpc.NewServer(sec.GRPCServerOptionMTLS(), grpc.KeepaliveParams(keepaliveParams))

	placementv1pb.RegisterPlacementServer(grpcServer, p)

	log.Infof("Placement service started on port %d", serverListener.Addr().(*net.TCPAddr).Port)

	errCh := make(chan error)
	go func() {
		errCh <- grpcServer.Serve(serverListener)
		log.Info("Placement service stopped")
	}()

	p.htarget.Ready()
	<-ctx.Done()

	if p.closed.CompareAndSwap(false, true) {
		close(p.closedCh)
	}

	grpcServer.GracefulStop()
	p.wg.Wait()

	return <-errCh
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
		// Runs when a stream is disconnected or when the placement service loses leadership
		p.streamConnGroup.Done()
		p.streamConnPool.delete(daprStream)
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

			if len(req.GetEntities()) == 0 {
				// is this an existing member that reported actor types before but now it has unregistered all of them?
				if !p.raftNode.FSM().State().HasMember(namespace, req.GetName(), req.GetId()) {
					continue
				}
				isActorRuntime = true
			} else {
				isActorRuntime = true
			}

			now := p.clock.Now()

			for _, entity := range req.GetEntities() {
				monitoring.RecordActorHeartbeat(req.GetId(), entity, req.GetName(), req.GetNamespace(), req.GetPod(), now)
			}

			// Record the heartbeat timestamp. Used for metrics and for disconnecting faulty hosts
			// on placement fail-over by comparing the member list in raft with the heartbeats
			p.lastHeartBeat.Store(req.GetNamespace()+"||"+req.GetName(), now.UnixNano())

			// Upsert incoming member only if the existing member info
			// doesn't match with the incoming member info.
			if p.raftNode.FSM().State().UpsertRequired(namespace, req) {
				entities := req.GetEntities()
				if entities == nil {
					entities = []string{}
				}
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberUpsert,
					host: raft.DaprHostMember{
						Name:      req.GetName(),
						AppID:     req.GetId(),
						Namespace: namespace,
						Entities:  entities,
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
			} else {
				log.Debugf("Stream connection is disconnected with the error: %v", err)
			}

			if isActorRuntime {
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberRemove,
					host:    raft.DaprHostMember{Name: registeredMemberID, Namespace: namespace},
				}
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
		return nil, status.Errorf(codes.PermissionDenied, "provided app ID %s doesn't match the one in the Spiffe ID (%s)", firstMessage.GetId(), clientID.AppID())
	}

	// For older versions that are not sending their namespace as part of the message
	// we will use the namespace from the Spiffe clientID
	if clientID != nil && firstMessage.GetNamespace() == "" {
		firstMessage.Namespace = clientID.Namespace()
	}

	if clientID != nil && firstMessage.GetNamespace() != clientID.Namespace() {
		return nil, status.Errorf(codes.PermissionDenied, "provided client namespace %s doesn't match the one in the Spiffe ID (%s)", firstMessage.GetNamespace(), clientID.Namespace())
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
	// If it is, we'll disseminate in the next disseminate interval
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
	clusterAPILevel := max(p.minAPILevel, p.raftNode.FSM().State().APILevel())

	if p.maxAPILevel != nil && clusterAPILevel > *p.maxAPILevel {
		clusterAPILevel = *p.maxAPILevel
	}

	// Ensure that the reported API level is at least equal to the current one in the cluster
	if req.GetApiLevel() < clusterAPILevel {
		return status.Errorf(codes.FailedPrecondition, "The cluster's Actor API level is %d, which is higher than the reported API level %d", clusterAPILevel, req.GetApiLevel())
	}

	return nil
}
