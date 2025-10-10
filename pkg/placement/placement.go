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
	"math"
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
	"github.com/dapr/kit/concurrency/cmap"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.placement")

const (
	// membershipChangeChSize is the channel size of membership change request from Dapr runtime.
	// MembershipChangeWorker will process actor host member change request.
	membershipChangeChSize = 10000

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
	hosts  []daprdStream
	tables *placementv1pb.PlacementTables
}

// GetVersion is used only for logs in membership.go
func (r *tablesUpdateRequest) GetVersion() string {
	if r.tables != nil {
		return r.tables.GetVersion()
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
	disseminateLocks cmap.Mutex[string]

	// keepaliveTime sets the interval at which the placement service sends keepalive pings to daprd
	// on the gRPC stream to check if the connection is still alive. Default is 2 seconds.
	// https://grpc.io/docs/guides/keepalive/
	keepAliveTime time.Duration

	// keepaliveTimeout sets the timeout period for daprd to respond to the placement service's
	// keepalive pings before the placement service closes the connection. Default is 3 seconds.
	// https://grpc.io/docs/guides/keepalive/
	keepAliveTimeout time.Duration

	// disseminateTimeout is the timeout to disseminate hashing tables after the membership change.
	// When the multiple actor service pods are deployed first, a few pods are deployed in the beginning
	// and the rest of pods will be deployed gradually. disseminateNextTime is maintained to decide when
	// the hashing table is disseminated. disseminateNextTime is updated whenever membership change
	// is applied to raft state or each pod is deployed. If we increase disseminateTimeout, it will
	// reduce the frequency of dissemination, but it will delay the table dissemination.
	// Default is 2 seconds
	disseminateTimeout time.Duration

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
	closedCh chan struct{}
	wg       sync.WaitGroup
}

// ServiceOpts contains options for the New method.
type ServiceOpts struct {
	MaxAPILevel        *uint32
	MinAPILevel        uint32
	SecProvider        security.Provider
	Port               int
	ListenAddress      string
	Healthz            healthz.Healthz
	KeepAliveTime      time.Duration
	KeepAliveTimeout   time.Duration
	DisseminateTimeout time.Duration
	Raft               raft.Options
}

func (o *ServiceOpts) SetMinAPILevel(apiLevel int) {
	if apiLevel >= 0 && apiLevel < math.MaxInt32 {
		o.MinAPILevel = uint32(apiLevel)
	}
}

func (o *ServiceOpts) SetMaxAPILevel(apiLevel int) {
	if apiLevel >= 0 && apiLevel < math.MaxInt32 {
		o.MaxAPILevel = ptr.Of(uint32(apiLevel))
	}
}

// New returns a new placement service.
func New(opts ServiceOpts) (*Service, error) {
	raftServer, err := raft.New(opts.Raft)
	if err != nil {
		return nil, fmt.Errorf("failed to create raft server: %w", err)
	}

	return &Service{
		streamConnPool:     newStreamConnPool(),
		membershipCh:       make(chan hostMemberChange, membershipChangeChSize),
		raftNode:           raftServer,
		maxAPILevel:        opts.MaxAPILevel,
		minAPILevel:        opts.MinAPILevel,
		clock:              &clock.RealClock{},
		closedCh:           make(chan struct{}),
		sec:                opts.SecProvider,
		disseminateLocks:   cmap.NewMutex[string](),
		memberUpdateCount:  *haxmap.New[string, *atomic.Uint32](),
		keepAliveTime:      opts.KeepAliveTime,
		keepAliveTimeout:   opts.KeepAliveTimeout,
		disseminateTimeout: opts.DisseminateTimeout,
		port:               opts.Port,
		listenAddress:      opts.ListenAddress,
		htarget:            opts.Healthz.AddTarget("placement-service"),
	}, nil
}

func (p *Service) Run(ctx context.Context) error {
	if !p.running.CompareAndSwap(false, true) {
		return errors.New("placement service is already running")
	}

	log.Info("Placement service is starting...")

	runners := []concurrency.Runner{
		p.runServer,
		p.raftNode.StartRaft,
		p.MonitorLeadership,
		func(ctx context.Context) error {
			<-ctx.Done()
			log.Info("Placement service is stopping...")
			close(p.closedCh)
			return nil
		},
	}

	return concurrency.NewRunnerManager(runners...).Run(ctx)
}

func (p *Service) runServer(ctx context.Context) error {
	defer p.htarget.NotReady()

	sec, err := p.sec.Handler(ctx)
	if err != nil {
		return err
	}

	serverListener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", p.listenAddress, p.port))
	if err != nil {
		return fmt.Errorf("failed to listen: %w", err)
	}

	keepaliveParams := keepalive.ServerParameters{
		Time:    p.keepAliveTime,
		Timeout: p.keepAliveTimeout,
	}

	grpcServer := grpc.NewServer(sec.GRPCServerOptionMTLS(), grpc.KeepaliveParams(keepaliveParams))

	placementv1pb.RegisterPlacementServer(grpcServer, p)

	log.Infof("Placement service started on port %d", serverListener.Addr().(*net.TCPAddr).Port)

	p.htarget.Ready()

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			log.Infof("Running Placement gRPC server on port %d", p.port)
			if err := grpcServer.Serve(serverListener); err != nil {
				return fmt.Errorf("failed to serve: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			<-ctx.Done()
			grpcServer.GracefulStop()
			log.Info("Placement GRPC server stopped")
			p.wg.Wait()
			return nil
		},
	).Run(ctx)
}

// ReportDaprStatus gets a heartbeat report from different Dapr hosts.
func (p *Service) ReportDaprStatus(stream placementv1pb.Placement_ReportDaprStatusServer) error { //nolint:nosnakecase
	if !p.hasLeadership.Load() {
		return status.Errorf(codes.FailedPrecondition, "node id=%s is not a leader. Only the leader can serve requests", p.raftNode.GetID())
	}

	var isActorHost atomic.Bool // Does the daprd sidecar host any actor types

	spiffeClientID, err := p.validateClient(stream)
	if err != nil {
		return err
	}

	firstMessage, err := stream.Recv()
	if err != nil {
		log.Errorf("Failed to receive the first message: %v", err)
		return status.Errorf(codes.Internal, "failed to receive the first message: %v", err)
	}

	err = p.validateFirstMessage(firstMessage, spiffeClientID)
	if err != nil {
		log.Errorf("First message validation failed: %v", err)
		return err
	}

	// Attach a new context to the stream, so that we can cancel it if there's a problem
	// during dissemination (outside of this go routine)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	daprStream := newDaprdStream(firstMessage, stream, cancel)
	p.streamConnGroup.Add(1)
	p.streamConnPool.add(daprStream)

	// Clean up when a stream is disconnected or when the placement service loses leadership
	defer func() {
		p.streamConnGroup.Done()
		p.streamConnPool.delete(daprStream)
	}()

	err = p.handleNewConnection(firstMessage, daprStream)
	if err != nil {
		log.Errorf("Handling new connection failed for host %s: %v", firstMessage.GetName(), err)
		return err
	}

	// Older versions won't be sending the namespace in subsequent messages either,
	// so we'll save this one in a separate variable
	namespace := firstMessage.GetNamespace()
	hostName := firstMessage.GetName()
	appID := firstMessage.GetId()

	// Read messages off the stream and send them to the recvCh
	go func() {
		defer func() {
			close(daprStream.recvCh)
		}()

		// Send the first message we read above
		if p.hasLeadership.Load() {
			select {
			case daprStream.recvCh <- recvResult{host: firstMessage, err: nil}:
				log.Debugf("received first message from %s: Id: %s, Name: %s, Namespace: %s, Actors: %s", hostName, appID, hostName, namespace, firstMessage.GetEntities())
			case <-ctx.Done():
				log.Debugf("stream connection is disconnected gracefully: %s", hostName)
				return
			}
		}

		// Start reading messages from the stream
		for p.hasLeadership.Load() {
			select {
			case <-ctx.Done():
				return
			default:
				req, recvErr := stream.Recv()
				// Check if the context has been cancelled in the meantime
				select {
				case <-ctx.Done():
					return
				case daprStream.recvCh <- recvResult{host: req, err: recvErr}:
					if recvErr != nil {
						return
					}
				}
			}
		}
	}()

	for p.hasLeadership.Load() {
		select {
		case <-ctx.Done():
			return p.handleErrorOnStream(ctx.Err(), firstMessage, &isActorHost)
		case <-p.closedCh:
			return errors.New("placement service is closed")
		case in := <-daprStream.recvCh:
			if in.err != nil {
				return p.handleErrorOnStream(in.err, firstMessage, &isActorHost)
			}

			host := in.host

			if !requiresUpdateInPlacementTables(host, &isActorHost) {
				continue
			}

			now := p.clock.Now()

			for _, entity := range host.GetEntities() {
				monitoring.RecordActorHeartbeat(host.GetId(), entity, host.GetName(), host.GetNamespace(), host.GetPod(), now)
			}

			// Record the heartbeat timestamp. Used for metrics and for disconnecting faulty hosts
			// on placement fail-over by comparing the member list in raft with the heartbeats
			p.lastHeartBeat.Store(host.GetNamespace()+"||"+host.GetName(), now.UnixNano())

			// Upsert incoming member only if the existing member info
			// doesn't match with the incoming member info.
			if p.raftNode.FSM().State().UpsertRequired(namespace, host) {
				log.Debugf("Sending member change command; upserting appid %s in namespace %s with entities %v", appID, namespace, host.GetEntities())
				p.membershipCh <- hostMemberChange{
					cmdType: raft.MemberUpsert,
					host: raft.DaprHostMember{
						Name:      host.GetName(),
						AppID:     appID,
						Namespace: namespace,
						Entities:  host.GetEntities(),
						UpdatedAt: now.UnixNano(),
						APILevel:  host.GetApiLevel(),
					},
				}
				log.Debugf("Member changed; upserting appid %s in namespace %s with entities %v", appID, namespace, host.GetEntities())
			}
		}
	}

	return status.Errorf(codes.FailedPrecondition, "node id=%s is not a leader. Only the leader can serve requests", p.raftNode.GetID())
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

func (p *Service) validateFirstMessage(firstMessage *placementv1pb.Host, clientID *spiffe.Parsed) error {
	if clientID != nil && firstMessage.GetId() != clientID.AppID() {
		return status.Errorf(codes.PermissionDenied, "provided app ID %s doesn't match the one in the Spiffe ID (%s)", firstMessage.GetId(), clientID.AppID())
	}

	// For older versions that are not sending their namespace as part of the message
	// we will use the namespace from the Spiffe clientID
	if clientID != nil && firstMessage.GetNamespace() == "" {
		firstMessage.Namespace = clientID.Namespace()
	}

	if clientID != nil && firstMessage.GetNamespace() != clientID.Namespace() {
		return status.Errorf(codes.PermissionDenied, "provided client namespace %s doesn't match the one in the Spiffe ID (%s)", firstMessage.GetNamespace(), clientID.Namespace())
	}

	if clientID != nil && firstMessage.GetId() != clientID.AppID() {
		log.Errorf("Client ID mismatch: %s != %s", firstMessage.GetId(), clientID.AppID())
		return status.Errorf(codes.PermissionDenied, "client ID %s is not allowed", firstMessage.GetId())
	}

	return nil
}

func (p *Service) handleNewConnection(req *placementv1pb.Host, daprStream *daprdStream) error {
	err := p.checkAPILevel(req)
	if err != nil {
		return err
	}

	// If the member is not an actor runtime (len(req.GetEntities()) == 0) we send it the placement table directly
	// This is safe to do without locking all the streams, because we're not changing the state
	// If the member does host actors, we need to update the state first, and then we'll disseminate in the next disseminate interval
	if len(req.GetEntities()) == 0 {
		updateReq := &tablesUpdateRequest{
			hosts: []daprdStream{*daprStream},
		}
		updateReq.tables = p.raftNode.FSM().PlacementState(req.GetNamespace())
		err = p.performTablesUpdate(context.Background(), updateReq)
		if err != nil {
			return err
		}
	}

	return nil
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

func (p *Service) handleErrorOnStream(err error, firstMessage *placementv1pb.Host, isActorHost *atomic.Bool) error {
	// Unwrap and check if it's a gRPC status error
	if st, ok := status.FromError(err); ok {
		if st.Code() == codes.Canceled {
			err = context.Canceled
		}
	}

	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		log.Infof("Stream connection for app %s is disconnected gracefully: %s", firstMessage.GetName(), err)
	} else {
		log.Errorf("Stream connection for app %s is disconnected with the error: %v.", firstMessage.GetName(), err)
	}

	if isActorHost.Load() {
		select {
		case p.membershipCh <- hostMemberChange{
			cmdType: raft.MemberRemove,
			host: raft.DaprHostMember{
				Name:      firstMessage.GetName(),
				Namespace: firstMessage.GetNamespace(),
				AppID:     firstMessage.GetId(),
			},
		}:
		case <-p.closedCh:
			return errors.New("placement service is closed")
		}
	}
	return nil
}

func requiresUpdateInPlacementTables(req *placementv1pb.Host, isActorHost *atomic.Bool) bool {
	// If the member is reporting no entities it's either a
	// - non-actor runtime or
	// - a host that used to have actors (isActorHost=true) but has now unregistered all its actors (common for workflows actors)
	// In the former case, we don't need to update the placement tables, because there are no member changes
	// In the latter case, we do need to update the placement tables, because we're removing a member that was previously registered

	reportsActors := len(req.GetEntities()) > 0
	existsInPlacementTables := isActorHost.Load() // if the member had previously reported actors
	isActorHost.Store(reportsActors || existsInPlacementTables)

	return reportsActors || existsInPlacementTables
}
