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

package main

import (
	"context"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/fswatcher"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/version"
)

var log = logger.NewLogger("dapr.placement")

const gracefulTimeout = 10 * time.Second

func main() {
	log.Infof("starting Dapr Placement Service -- version %s -- commit %s", version.Version(), version.Commit())

	cfg := newConfig()

	// Apply options to all loggers.
	if err := logger.ApplyOptionsToLoggers(&cfg.loggerOptions); err != nil {
		log.Fatal(err)
	}
	log.Infof("log level set to: %s", cfg.loggerOptions.OutputLevel)

	// Initialize dapr metrics for placement.
	if err := cfg.metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}

	// Start Raft cluster.
	raftServer := raft.New(cfg.raftID, cfg.raftInMemEnabled, cfg.raftPeers, cfg.raftLogStorePath)
	if raftServer == nil {
		log.Fatal("failed to create raft server.")
	}

	if err := raftServer.StartRaft(nil); err != nil {
		log.Fatalf("failed to start Raft Server: %v", err)
	}

	// Start Placement gRPC server.
	hashing.SetReplicationFactor(cfg.replicationFactor)
	apiServer := placement.NewPlacementService(raftServer)
	var certChain *credentials.CertChain
	if cfg.tlsEnabled {
		certChain = loadCertChains(cfg.certChainPath)
	}

	go apiServer.MonitorLeadership()
	go apiServer.Run(strconv.Itoa(cfg.placementPort), certChain)
	log.Infof("placement service started on port %d", cfg.placementPort)

	// Start Healthz endpoint.
	go startHealthzServer(cfg.healthzPort)

	// Relay incoming process signal to exit placement gracefully
	signalCh := make(chan os.Signal, 10)
	signal.Notify(signalCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(signalCh)

	<-signalCh

	// Shutdown servers
	gracefulExitCh := make(chan struct{})
	go func() {
		apiServer.Shutdown()
		raftServer.Shutdown()
		close(gracefulExitCh)
	}()

	select {
	case <-time.After(gracefulTimeout):
		log.Info("Timeout on graceful leave. Exiting...")
		os.Exit(1)

	case <-gracefulExitCh:
		log.Info("Gracefully exit.")
		os.Exit(0)
	}
}

func startHealthzServer(healthzPort int) {
	healthzServer := health.NewServer(log)
	healthzServer.Ready()

	if err := healthzServer.Run(context.Background(), healthzPort); err != nil {
		log.Fatalf("failed to start healthz server: %s", err)
	}
}

func loadCertChains(certChainPath string) *credentials.CertChain {
	tlsCreds := credentials.NewTLSCredentials(certChainPath)

	log.Info("mTLS enabled, getting tls certificates")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	fsevent := make(chan struct{})
	go func() {
		log.Infof("starting watch for certs on filesystem: %s", certChainPath)
		err := fswatcher.Watch(ctx, tlsCreds.Path(), fsevent)
		if err != nil {
			log.Fatalf("error starting watch on filesystem: %s", err)
		}
		close(fsevent)
		if ctx.Err() == context.DeadlineExceeded {
			log.Fatal("timeout while waiting to load tls certificates")
		}
	}()
	for {
		chain, err := credentials.LoadFromDisk(tlsCreds.RootCertPath(), tlsCreds.CertPath(), tlsCreds.KeyPath())
		if err == nil {
			log.Info("tls certificates loaded successfully")
			return chain
		}
		log.Info("tls certificate not found; waiting for disk changes")
		<-fsevent
		log.Debug("watcher found activity on filesystem")
	}
}
