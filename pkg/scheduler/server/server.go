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
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/scheduler"
	"github.com/dapr/dapr/pkg/scheduler/connections"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"

	etcdcron "github.com/Scalingo/go-etcd-cron"

	"github.com/dapr/dapr/pkg/actors"
	"github.com/dapr/dapr/pkg/actors/config"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	globalconfig "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server")

// Connection pool for namespace/appID separation of sidecars to schedulers
//type connPool struct {
//	lock             sync.RWMutex
//	nsappidPool      map[string]*appidPool
//	minConnsPerAppID map[string]int
//}

//type appidPool struct {
//	lock sync.RWMutex
//	//connected []chan struct{}
//	connected []*sidecarConnDetails
//}

// Add a channel to the pool for a given namespace/appID
// func (p *connPool) add(nsappid string, ch chan struct{}) {
//func (p *connPool) add(nsappid string, connDetails *sidecarConnDetails) {
//	p.lock.Lock()
//	defer p.lock.Unlock()
//	if id, ok := p.nsappidPool[nsappid]; ok { //exists
//		id.lock.Lock()
//		defer id.lock.Unlock()
//		id.connected = append(id.connected, connDetails)
//	} else { //doesn't exist
//		p.nsappidPool[nsappid] = &appidPool{
//			connected: []*sidecarConnDetails{connDetails},
//			//connected: []chan struct{}{ch},
//		}
//	}
//}

// Remove a channel from the pool for a given namespace/appID
// func (p *connPool) remove(nsappid string, ch chan struct{}) {
//func (p *connPool) remove(nsappid string, connDetails *sidecarConnDetails) {
//	p.lock.Lock()
//	defer p.lock.Unlock()
//	if id, ok := p.nsappidPool[nsappid]; ok {
//		id.lock.Lock()
//		defer id.lock.Unlock()
//		for i, c := range id.connected {
//			if c == connDetails {
//				id.connected = append(id.connected[:i], id.connected[i+1:]...)
//				break
//			}
//		}
//	}
//}

type Options struct {
	AppID                  string
	HostAddress            string
	ListenAddress          string
	PlacementAddress       string
	Mode                   modes.DaprMode
	Port                   int
	MinConnsPerAppID       int
	MaxTimeWaitForSidecars int

	DataDir          string
	EtcdID           string
	EtcdInitialPeers []string
	EtcdClientPorts  []string

	Security security.Handler
}

// Server is the gRPC server for the Scheduler service.
type Server struct {
	port          int
	srv           *grpc.Server
	listenAddress string

	dataDir          string
	etcdID           string
	etcdInitialPeers []string
	etcdClientPorts  map[string]string
	cron             *etcdcron.Cron
	readyCh          chan struct{}
	jobTriggerChan   chan *runtimev1pb.Job // used to trigger the WatchJob logic

	sidecarConnChan        chan *scheduler.SidecarConnDetails
	poolLock               sync.RWMutex
	connectionPool         *connections.Pool // Connection pool for sidecars
	minConnPerApp          int
	maxTimeWaitForSidecars int

	grpcManager  *manager.Manager
	actorRuntime actors.ActorRuntime
}

func New(opts Options) *Server {
	clientPorts := make(map[string]string)
	for _, input := range opts.EtcdClientPorts {
		idAndPort := strings.Split(input, "=")
		if len(idAndPort) != 2 {
			log.Warnf("Incorrect format for client ports: %s. Should contain <id>=<client-port>", input)
			continue
		}
		schedulerID := strings.TrimSpace(idAndPort[0])
		port := strings.TrimSpace(idAndPort[1])
		clientPorts[schedulerID] = port
	}

	s := &Server{
		port:          opts.Port,
		listenAddress: opts.ListenAddress,

		etcdID:           opts.EtcdID,
		etcdInitialPeers: opts.EtcdInitialPeers,
		etcdClientPorts:  clientPorts,
		dataDir:          opts.DataDir,
		readyCh:          make(chan struct{}),
		jobTriggerChan:   make(chan *runtimev1pb.Job), //probably slice here

		sidecarConnChan: make(chan *scheduler.SidecarConnDetails),
		connectionPool: &connections.Pool{
			NsAppIDPool:      make(map[string]*connections.AppIDPool),
			MinConnsPerAppID: make(map[string]int),
		},
		minConnPerApp:          opts.MinConnsPerAppID,
		maxTimeWaitForSidecars: opts.MaxTimeWaitForSidecars,
	}

	s.srv = grpc.NewServer(opts.Security.GRPCServerOptionMTLS())
	schedulerv1pb.RegisterSchedulerServer(s.srv, s)

	apiLevel := &atomic.Uint32{}
	apiLevel.Store(config.ActorAPILevel)

	if opts.PlacementAddress != "" {
		// Create gRPC manager
		grpcAppChannelConfig := &manager.AppChannelConfig{}
		s.grpcManager = manager.NewManager(opts.Security, opts.Mode, grpcAppChannelConfig)
		s.grpcManager.StartCollector()

		act, _ := actors.NewActors(actors.ActorsOpts{
			AppChannel:       nil,
			GRPCConnectionFn: s.grpcManager.GetGRPCConnection,
			Config: actors.Config{
				Config: config.Config{
					ActorsService:                 "placement:" + opts.PlacementAddress,
					AppID:                         opts.AppID,
					HostAddress:                   opts.HostAddress,
					Port:                          s.port,
					PodName:                       os.Getenv("POD_NAME"),
					HostedActorTypes:              config.NewHostedActors([]string{}),
					ActorDeactivationScanInterval: time.Hour, // TODO: disable this feature since we just need to invoke actors
				},
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
		s.runEtcd,
		s.runJobWatcher,
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

func (s *Server) runEtcd(ctx context.Context) error {
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

	c, err := etcdcron.NewEtcdMutexBuilder(clientv3.Config{Endpoints: etcdEndpoints})
	if err != nil {
		return err
	}

	// pass in initial cluster endpoints, but with client ports
	cron, err := etcdcron.New(etcdcron.WithEtcdMutexBuilder(c))
	if err != nil {
		return fmt.Errorf("fail to create etcd-cron: %s", err)
	}

	cron.Start(ctx)
	defer cron.Stop()

	s.cron = cron
	close(s.readyCh)

	select {
	case err := <-etcd.Err():
		return err
	case <-ctx.Done():
		log.Info("Embedded Etcd shutting down")
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

// runJobWatcher (dynamically) watches for (client) sidecar connections and add them to the connection pool.
func (s *Server) runJobWatcher(ctx context.Context) error {
	log.Infof("Starting job watcher")

	// Set up a timer for the overall timeout
	//overallTimeout := time.After(time.Duration(s.maxTimeWaitForSidecars) * time.Second)

	// create initial pool of appIDs for minConnCount for streaming
	// this needs to watch for client, sidecar connections and add
	for {
		select {
		case <-ctx.Done():
			log.Info("Job watcher shutting down")
			return ctx.Err()
		//case <-overallTimeout: // check: do we want this?
		//	log.Error("Overall timeout waiting for any sidecar connection")
		//	return fmt.Errorf("overall timeout waiting for any sidecar connection")
		case connDetails := <-s.sidecarConnChan:
			log.Infof("Adding a Sidecar connection to Scheduler")
			nsAppID := connDetails.Namespace + connDetails.AppID
			// Add sidecar connection details to the connection pool
			s.connectionPool.Add(nsAppID, connDetails)

			// Wait until reaching the minimum connection count
			if err := s.connectionPool.WaitUntilReachingMinConns(ctx, nsAppID, s.minConnPerApp, time.Duration(s.maxTimeWaitForSidecars)*time.Second); err != nil {
				// If there's an error waiting for minimum connection count
				return err
			}
		}
	}
}

// addToConnectionPool adds sidecar connection details to the connection pool.
//func (s *Server) addToConnectionPool(connDetails *SidecarConnDetails) {
//	s.poolLock.Lock()
//	defer s.poolLock.Unlock()
//
//	nsAppID := connDetails.namespace + connDetails.appID
//
//	// Initialize connection pool for the namespace/appID if it doesn't exist
//	if _, ok := s.connectionPool.NsAppIDPool[nsAppID]; !ok {
//		s.connectionPool.NsAppIDPool[nsAppID] = &connections.AppIDPool{
//			Connected: []*SidecarConnDetails{connDetails},
//		}
//	} else {
//		s.connectionPool.Add(nsAppID, connDetails)
//	}
//
//	log.Infof("Added sidecar connection to the pool: Host: %s, Namespace: %s, Port: %d", connDetails.host, connDetails.namespace, connDetails.port)
//}

// waitUntilReachingMinConns waits until the minimum connection count is reached for a given namespace/appID.
//func (s *Server) waitUntilReachingMinConns(ctx context.Context, nsAppID string) error {
//	// Lock the connection pool for reading
//	s.poolLock.RLock()
//	defer s.poolLock.RUnlock()
//
//	// Wait until the minimum connection count is reached
//	for {
//		// Get the current connection count
//		currentConnCount := len(s.connectionPool.NsAppIDPool[nsAppID].connected)
//
//		// If the current connection count is greater than or equal to the minimum required, break the loop
//		if currentConnCount >= s.minConnPerApp {
//			break
//		}
//
//		// Wait for a short duration before checking again
//		select {
//		case <-ctx.Done():
//			return ctx.Err()
//		case <-time.After(time.Duration(s.maxTimeWaitForSidecars) * time.Second):
//			continue
//		}
//	}
//
//	return nil
//}
