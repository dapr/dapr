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
	"fmt"
	"strconv"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/hashing"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.placement")

func main() {
	log.Infof("Starting Dapr Placement Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	cfg := newConfig()

	// Apply options to all loggers.
	if err := logger.ApplyOptionsToLoggers(&cfg.loggerOptions); err != nil {
		log.Fatal(err)
	}
	log.Infof("Log level set to: %s", cfg.loggerOptions.OutputLevel)

	// Initialize dapr metrics for placement.
	err := cfg.metricsExporter.Init()
	if err != nil {
		log.Fatal(err)
	}

	err = monitoring.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	// Start Raft cluster.
	raftServer := raft.New(raft.Options{
		ID:           cfg.raftID,
		InMem:        cfg.raftInMemEnabled,
		Peers:        cfg.raftPeers,
		LogStorePath: cfg.raftLogStorePath,
	})
	if raftServer == nil {
		log.Fatal("Failed to create raft server.")
	}

	var certChain *credentials.CertChain
	if cfg.tlsEnabled {
		tlsCreds := credentials.NewTLSCredentials(cfg.certChainPath)

		certChain, err = credentials.LoadFromDisk(tlsCreds.RootCertPath(), tlsCreds.CertPath(), tlsCreds.KeyPath())
		if err != nil {
			log.Fatal(err)
		}

		log.Info("TLS certificates loaded successfully")
	}

	// Start Placement gRPC server.
	hashing.SetReplicationFactor(cfg.replicationFactor)
	apiServer, err := placement.NewPlacementService(raftServer, certChain)
	if err != nil {
		log.Fatal(err)
	}

	err = concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			return raftServer.StartRaft(ctx, nil)
		},
		apiServer.MonitorLeadership,
		func(ctx context.Context) error {
			healthzServer := health.NewServer(log)
			healthzServer.Ready()
			if healthzErr := healthzServer.Run(ctx, cfg.healthzPort); healthzErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", healthzErr)
			}
			return nil
		},
		func(ctx context.Context) error {
			return apiServer.Run(ctx, strconv.Itoa(cfg.placementPort))
		},
	).Run(signals.Context())
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Placement service shut down gracefully")
}
