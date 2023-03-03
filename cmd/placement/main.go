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
	log.Infof("starting Dapr Placement Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

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

	// Start Placement gRPC server.
	hashing.SetReplicationFactor(cfg.replicationFactor)
	apiServer := placement.NewPlacementService(raftServer)

	var certChain *credentials.CertChain
	if cfg.tlsEnabled {
		tlsCreds := credentials.NewTLSCredentials(cfg.certChainPath)

		var err error
		certChain, err = credentials.LoadFromDisk(tlsCreds.RootCertPath(), tlsCreds.CertPath(), tlsCreds.KeyPath())
		if err != nil {
			log.Fatal(err)
		}

		log.Info("tls certificates loaded successfully")
	}

	if err := concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			return raftServer.StartRaft(ctx, nil)
		},
		apiServer.MonitorLeadership,
		func(ctx context.Context) error {
			healthzServer := health.NewServer(log)
			healthzServer.Ready()
			if err := healthzServer.Run(ctx, cfg.healthzPort); err != nil {
				return fmt.Errorf("failed to start healthz server: %w", err)
			}
			return nil
		},
		func(ctx context.Context) error {
			return apiServer.Run(ctx, strconv.Itoa(cfg.placementPort), certChain)
		},
	).Run(signals.Context()); err != nil {
		log.Fatal(err)
	}

	log.Info("placement service shut down gracefully")
}
