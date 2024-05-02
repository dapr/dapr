/*
Copyright 2023 The Dapr Authors
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

package server

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync"

	etcdcron "github.com/diagridio/go-etcd-cron"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	globalconfig "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/scheduler/server/internal"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

type Options struct {
	AppID            string
	HostAddress      string
	ListenAddress    string
	PlacementAddress string
	Mode             modes.DaprMode
	Port             int
	Security         security.Handler

	DataDir                 string
	Id                      string
	EtcdInitialPeers        []string
	EtcdClientPorts         []string
	EtcdClientHttpPorts     []string
	EtcdSpaceQuota          int64
	EtcdCompactionMode      string
	EtcdCompactionRetention string
}

// Server is the gRPC server for the Scheduler service.
type Server struct {
	port          int
	srv           *grpc.Server
	listenAddress string
	mode          modes.DaprMode

	dataDir                 string
	id                      string
	etcdInitialPeers        []string
	etcdClientPorts         map[string]string
	etcdClientHttpPorts     map[string]string
	etcdSpaceQuota          int64
	etcdCompactionMode      string
	etcdCompactionRetention string
	cron                    *etcdcron.Cron
	readyCh                 chan struct{}
	jobTriggerChan          chan *schedulerv1pb.WatchJobsResponse // used to trigger the WatchJobs logic
	jobWatcherWG            sync.WaitGroup

	sidecarConnChan chan *internal.Connection
	connectionPool  *internal.Pool // Connection pool for sidecars
	closeCh         chan struct{}

	grpcManager  *manager.Manager
	actorRuntime actors.ActorRuntime
}

func New(opts Options) *Server {
	clientPorts := parseClientPorts(opts.EtcdClientPorts)
	clientHttpPorts := parseClientPorts(opts.EtcdClientHttpPorts)

	s := &Server{
		port:          opts.Port,
		listenAddress: opts.ListenAddress,
		mode:          opts.Mode,

		id:                      opts.Id,
		etcdInitialPeers:        opts.EtcdInitialPeers,
		etcdClientPorts:         clientPorts,
		etcdClientHttpPorts:     clientHttpPorts,
		etcdSpaceQuota:          opts.EtcdSpaceQuota,
		etcdCompactionMode:      opts.EtcdCompactionMode,
		etcdCompactionRetention: opts.EtcdCompactionRetention,
		dataDir:                 opts.DataDir,
		readyCh:                 make(chan struct{}),
		jobTriggerChan:          make(chan *schedulerv1pb.WatchJobsResponse),
		jobWatcherWG:            sync.WaitGroup{},

		sidecarConnChan: make(chan *internal.Connection),
		connectionPool: &internal.Pool{
			NsAppIDPool: make(map[string]*internal.AppIDPool),
		},
		closeCh: make(chan struct{}),
	}

	s.srv = grpc.NewServer(opts.Security.GRPCServerOptionMTLS())
	schedulerv1pb.RegisterSchedulerServer(s.srv, s)

	if opts.PlacementAddress != "" {
		// Create gRPC manager
		grpcAppChannelConfig := &manager.AppChannelConfig{}
		s.grpcManager = manager.NewManager(opts.Security, opts.Mode, grpcAppChannelConfig)
		s.grpcManager.StartCollector()

		act, _ := actors.NewActors(actors.ActorsOpts{
			AppChannel:       nil,
			GRPCConnectionFn: s.grpcManager.GetGRPCConnection,
			Config:           actors.Config{
				// This will be rm-ed in the next PR
				//Config: actorInternal.Config{
				//	ActorsService:    "placement:" + opts.PlacementAddress,
				//	AppID:            opts.AppID,
				//	HostAddress:      opts.HostAddress,
				//	Port:             s.port,
				//	PodName:          os.Getenv("POD_NAME"),
				//	HostedActorTypes: actorInternal.NewHostedActors([]string{}),
				//},
			},
			TracingSpec:     globalconfig.TracingSpec{},
			Resiliency:      resiliency.New(log),
			StateStoreName:  "",
			CompStore:       nil,
			StateTTLEnabled: false, // artursouza: this should not be relevant to invoke actors.
			Security:        opts.Security,
		})

		s.actorRuntime = act
	}
	return s
}

func parseClientPorts(opts []string) map[string]string {
	ports := make(map[string]string)

	for _, input := range opts {
		idAndPort := strings.Split(input, "=")
		if len(idAndPort) != 2 {
			log.Warnf("Incorrect format for client http ports: %s. Should contain <id>=<client-port>", input)
			continue
		}
		schedulerID := strings.TrimSpace(idAndPort[0])
		port := strings.TrimSpace(idAndPort[1])
		ports[schedulerID] = port
	}

	return ports
}

func (s *Server) Run(ctx context.Context) error {
	log.Info("Dapr Scheduler is starting...")

	if s.actorRuntime != nil {
		log.Info("Initializing actor runtime")
		err := s.actorRuntime.Init(ctx)
		if err != nil {
			return err
		}
	}
	return concurrency.NewRunnerManager(
		s.runServer,
		s.runEtcdCron,
		s.runJobWatcher,
		func(ctx context.Context) error {
			<-ctx.Done()
			close(s.closeCh)
			return nil
		},
	).Run(ctx)
}

func (s *Server) runServer(ctx context.Context) error {
	var listener net.Listener
	var err error

	if s.listenAddress != "" {
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:%d", s.listenAddress, s.port))
		if err != nil {
			return fmt.Errorf("could not listen on port %d: %w", s.port, err)
		}
		log.Infof("Dapr Scheduler listening on: %s:%d", s.listenAddress, s.port)
	} else {
		listener, err = net.Listen("tcp", fmt.Sprintf(":%d", s.port))
		if err != nil {
			return fmt.Errorf("could not listen on port %d: %w", s.port, err)
		}
		log.Infof("Dapr Scheduler listening on port :%d", s.port)
	}

	errCh := make(chan error)
	go func() {
		log.Infof("Running gRPC server on port %d", s.port)
		if nerr := s.srv.Serve(listener); nerr != nil {
			errCh <- fmt.Errorf("failed to serve: %w", nerr)
			return
		}
	}()

	select {
	case err = <-errCh:
		return err
	case <-ctx.Done():
		s.srv.GracefulStop()
		log.Info("Scheduler GRPC server stopped")
		return nil
	}
}

func (s *Server) runEtcdCron(ctx context.Context) error {
	log.Info("Starting etcd")

	etcd, err := embed.StartEtcd(s.conf())
	if err != nil {
		return err
	}
	defer etcd.Close()

	select {
	case <-etcd.Server.ReadyNotify():
		log.Info("Etcd server is ready!")
	case <-ctx.Done():
		return ctx.Err()
	}

	log.Info("Starting EtcdCron")

	etcdEndpoints := clientEndpoints(s.etcdInitialPeers, s.etcdClientPorts)

	c, err := clientv3.New(clientv3.Config{Endpoints: etcdEndpoints})
	if err != nil {
		return err
	}

	// pass in initial cluster endpoints, but with client ports
	cron, err := etcdcron.New(
		etcdcron.WithEtcdClient(c),
		etcdcron.WithTriggerFunc(s.triggerJob),
	)
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}

	cron.Start(ctx)

	s.cron = cron
	close(s.readyCh)

	select {
	case err := <-etcd.Err():
		return err
	case <-ctx.Done():
		log.Info("Embedded Etcd shutting down")
		cron.Wait()
		// Don't close etcd here because it has a defer close() already.
		return nil
	}
}

func clientEndpoints(initialPeersListIP []string, idToPort map[string]string) []string {
	clientEndpoints := make([]string, 0)
	for _, scheduler := range initialPeersListIP {
		idAndAddress := strings.Split(scheduler, "=")
		if len(idAndAddress) != 2 {
			log.Warnf("Incorrect format for initialPeerList: %s. Should contain <id>=http://<ip>:<peer-port>", initialPeersListIP)
			continue
		}

		id := strings.TrimSpace(idAndAddress[0])
		clientPort, ok := idToPort[id]
		if !ok {
			log.Warnf("Unable to find port from initialPeerList: %s. Should contain <id>=http://<ip>:<peer-port>", initialPeersListIP)
			continue
		}

		address := strings.TrimSpace(idAndAddress[1])
		u, err := url.Parse(address)
		if err != nil {
			log.Warnf("Unable to parse url from initialPeerList: %s. Should contain <id>=http://<ip>:<peer-port>", initialPeersListIP)
			continue
		}

		updatedURL := fmt.Sprintf("%s:%s", u.Hostname(), clientPort)

		clientEndpoints = append(clientEndpoints, updatedURL)
	}
	return clientEndpoints
}

// runJobWatcher (dynamically) watches for (client) sidecar connections and adds them to the connection pool.
func (s *Server) runJobWatcher(ctx context.Context) error {
	log.Infof("Starting job watcher")

	s.jobWatcherWG.Add(2)

	// Goroutine for handling sidecar connections
	go func() {
		defer log.Info("Sidecar connections shutting down.")
		defer s.jobWatcherWG.Done()
		s.handleSidecarConnections(ctx)
	}()

	// Goroutine for handling job streaming at trigger time
	go func() {
		defer log.Info("Job streaming shutting down.")
		defer s.jobWatcherWG.Done()
		s.handleJobStreaming(ctx)
	}()

	<-ctx.Done()
	s.jobWatcherWG.Wait()
	log.Info("JobWatcher exited")
	return ctx.Err()
}

// handleJobStreaming handles the streaming of jobs to Dapr sidecars.
func (s *Server) handleJobStreaming(ctx context.Context) {
	for {
		select {
		case job := <-s.jobTriggerChan:
			// TODO(CASSIE): once the ns + appID is sent back on the triggerJob,
			// dont use metadata to lookup those fields

			metadata := job.GetMetadata()
			appID := metadata["appID"]
			namespace := metadata["namespace"]

			// Pick a stream corresponding to the appID
			stream, err := s.connectionPool.GetStreamAndContextForNSAppID(namespace, appID)
			if err != nil {
				log.Debugf("Error getting stream for appID: %v", err)
				// TODO: add job to a queue or something to try later
				// this should be another long running go routine that accepts this job on a channel
				continue
			}

			// Send the job update to the sidecar
			if err := stream.Send(job); err != nil {
				log.Warnf("Error sending job at trigger time: %v", err)
				// TODO: add job to a queue or something to try later
				// this should be another long running go routine that accepts this job on a channel
			}
		case <-ctx.Done():
			return
		}
	}
}

// handleSidecarConnections handles the (client) sidecar connections and adds them to the connection pool.
func (s *Server) handleSidecarConnections(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			s.connectionPool.Cleanup()
			return
		case conn := <-s.sidecarConnChan:
			log.Infof("Adding a Sidecar connection to Scheduler for appID: %s.", conn.AppID)

			// Add sidecar connection details to the connection pool
			s.connectionPool.Add(conn.Namespace, conn.AppID, conn)
		}
	}
}
