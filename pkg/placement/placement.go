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
	"google.golang.org/grpc/status"
	"k8s.io/utils/clock"

	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	placementv1pb "github.com/dapr/dapr/pkg/proto/placement/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/pkg/security/spiffe"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement")

type placementGRPCStream placementv1pb.Placement_ReportDaprStatusServer //nolint:nosnakecase

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

// Service updates the Dapr runtimes with distributed hash tables for stateful entities.
type Service struct {
	// streamConnPool has the stream connections established between placement gRPC server and Dapr runtime.
	streamConnPool []placementGRPCStream

	// streamConnPoolLock is the lock for streamConnPool change.
	streamConnPoolLock sync.RWMutex

	// raftNode is the raft server instance.
	raftNode *raft.Server

	// lastHeartBeat represents the last time stamp when runtime sent heartbeat.
	lastHeartBeat sync.Map
	// membershipCh is the channel to maintain Dapr runtime host membership update.
	membershipCh chan hostMemberChange
	// disseminateLock is the lock for hashing table dissemination.
	disseminateLock sync.Mutex
	// disseminateNextTime is the time when the hashing tables are disseminated.
	disseminateNextTime atomic.Int64
	// memberUpdateCount represents how many dapr runtimes needs to change.
	// consistent hashing table. Only actor runtime's heartbeat will increase this.
	memberUpdateCount atomic.Uint32

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
		streamConnPool:           []placementGRPCStream{},
		membershipCh:             make(chan hostMemberChange, membershipChangeChSize),
		faultyHostDetectDuration: fhdd,
		raftNode:                 opts.RaftNode,
		maxAPILevel:              opts.MaxAPILevel,
		minAPILevel:              opts.MinAPILevel,
		clock:                    &clock.RealClock{},
		closedCh:                 make(chan struct{}),
		sec:                      opts.SecProvider,
	}
}

// Run starts the placement service gRPC server.
// Blocks until the service is closed and all connections are drained.
func (p *Service) Run(ctx context.Context, port string) error {
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

	serverListener, err := net.Listen("tcp", fmt.Sprintf(":%s", port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}
	grpcServer := grpc.NewServer(sec.GRPCServerOptionMTLS())

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

// ReportDaprStatus gets a heartbeat report from different Dapr hosts.
func (p *Service) ReportDaprStatus(stream placementv1pb.Placement_ReportDaprStatusServer) error { //nolint:nosnakecase
	registeredMemberID := ""
	isActorRuntime := false

	sec, err := p.sec.Handler(stream.Context())
	if err != nil {
		return status.Errorf(codes.Internal, "")
	}

	var clientID *spiffe.Parsed
	if sec.MTLSEnabled() {
		id, ok, err := spiffe.FromGRPCContext(stream.Context())
		if err != nil || !ok {
			log.Debugf("failed to get client ID from context: err=%v, ok=%t", err, ok)
			return status.Errorf(codes.Unauthenticated, "failed to get client ID from context")
		}
		clientID = id
	}

	p.streamConnGroup.Add(1)
	defer func() {
		p.streamConnGroup.Done()
		p.deleteStreamConn(stream)
	}()

	for p.hasLeadership.Load() {
		req, err := stream.Recv()
		switch err {
		case nil:
			if clientID != nil && req.GetId() != clientID.AppID() {
				return status.Errorf(codes.PermissionDenied, "client ID %s is not allowed", req.GetId())
			}

			state := p.raftNode.FSM().State()

			if registeredMemberID == "" {
				// New connection
				// Ensure that the reported API level is at least equal to the current one in the cluster
				clusterAPILevel := state.APILevel()
				if clusterAPILevel < p.minAPILevel {
					clusterAPILevel = p.minAPILevel
				}
				if p.maxAPILevel != nil && clusterAPILevel > *p.maxAPILevel {
					clusterAPILevel = *p.maxAPILevel
				}
				if req.GetApiLevel() < clusterAPILevel {
					return status.Errorf(codes.FailedPrecondition, "The cluster's Actor API level is %d, which is higher than the reported API level %d", clusterAPILevel, req.GetApiLevel())
				}

				registeredMemberID = req.GetName()
				p.addStreamConn(stream)
				// We need to use a background context here so dissemination isn't tied to the context of this stream
				placementTable := p.raftNode.FSM().PlacementState()

				err = p.performTablesUpdate(context.Background(), []placementGRPCStream{stream}, placementTable)
				if err != nil {
					return err
				}
				log.Debugf("Stream connection is established from %s", registeredMemberID)
			}

			// Ensure that the incoming runtime is actor instance.
			isActorRuntime = len(req.GetEntities()) > 0
			if !isActorRuntime {
				// ignore if this runtime is non-actor.
				continue
			}

			now := p.clock.Now()

			for _, entity := range req.GetEntities() {
				monitoring.RecordActorHeartbeat(req.GetId(), entity, req.GetName(), req.GetPod(), now)
			}

			// Record the heartbeat timestamp. This timestamp will be used to check if the member
			// state maintained by raft is valid or not. If the member is outdated based the timestamp
			// the member will be marked as faulty node and removed.
			p.lastHeartBeat.Store(req.GetName(), now.UnixNano())

			members := state.Members()

			// Upsert incoming member only if it is an actor service (not actor client) and
			// the existing member info is unmatched with the incoming member info.
			upsertRequired := true
			if m, ok := members[req.GetName()]; ok {
				if m.AppID == req.GetId() && m.Name == req.GetName() && cmp.Equal(m.Entities, req.GetEntities()) {
					upsertRequired = false
				}
			}

			if upsertRequired {
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberUpsert,
					host: raft.DaprHostMember{
						Name:      req.GetName(),
						AppID:     req.GetId(),
						Entities:  req.GetEntities(),
						UpdatedAt: now.UnixNano(),
						APILevel:  req.GetApiLevel(),
					},
				}
				log.Debugf("Member changed upserting appid %s with entities %v", req.GetId(), req.GetEntities())
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
						host:    raft.DaprHostMember{Name: registeredMemberID},
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

// addStreamConn adds stream connection between runtime and placement to the dissemination pool.
func (p *Service) addStreamConn(conn placementGRPCStream) {
	p.streamConnPoolLock.Lock()
	p.streamConnPool = append(p.streamConnPool, conn)
	p.streamConnPoolLock.Unlock()
}

func (p *Service) deleteStreamConn(conn placementGRPCStream) {
	p.streamConnPoolLock.Lock()
	for i, c := range p.streamConnPool {
		if c == conn {
			p.streamConnPool = append(p.streamConnPool[:i], p.streamConnPool[i+1:]...)
			break
		}
	}
	p.streamConnPoolLock.Unlock()
}

func (p *Service) hasStreamConn(conn placementGRPCStream) bool {
	p.streamConnPoolLock.RLock()
	defer p.streamConnPoolLock.RUnlock()

	for _, c := range p.streamConnPool {
		if c == conn {
			return true
		}
	}
	return false
}
