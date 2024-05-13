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

package app

import (
	"context"
	"fmt"
	"math"
	"os"
	"strconv"

	"github.com/dapr/dapr/cmd/placement/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
	"github.com/dapr/kit/signals"
)

var log = logger.NewLogger("dapr.placement")

func Run() {
	opts := options.New(os.Args[1:])

	// Apply options to all loggers.
	if err := logger.ApplyOptionsToLoggers(&opts.Logger); err != nil {
		log.Fatal(err)
	}

	log.Infof("Starting Dapr Placement Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("Log level set to: %s", opts.Logger.OutputLevel)

	metricsExporter := metrics.NewExporterWithOptions(log, metrics.DefaultMetricNamespace, opts.Metrics)

	err := monitoring.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	// Start Raft cluster.
	raftServer := raft.New(raft.Options{
		ID:                opts.RaftID,
		InMem:             opts.RaftInMemEnabled,
		Peers:             opts.RaftPeers,
		LogStorePath:      opts.RaftLogStorePath,
		ReplicationFactor: int64(opts.ReplicationFactor),
		MinAPILevel:       uint32(opts.MinAPILevel),
		MaxAPILevel:       uint32(opts.MaxAPILevel),
	})
	if raftServer == nil {
		log.Fatal("Failed to create raft server.")
	}

	ctx := signals.Context()
	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           opts.SentryAddress,
		ControlPlaneTrustDomain: opts.TrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        opts.TrustAnchorsFile,
		AppID:                   "dapr-placement",
		MTLSEnabled:             opts.TLSEnabled,
		Mode:                    modes.DaprMode(opts.Mode),
	})
	if err != nil {
		log.Fatal(err)
	}

	placementOpts := placement.PlacementServiceOpts{
		RaftNode:    raftServer,
		SecProvider: secProvider,
	}
	if opts.MinAPILevel >= 0 && opts.MinAPILevel < math.MaxInt32 {
		placementOpts.MinAPILevel = uint32(opts.MinAPILevel)
	}
	if opts.MaxAPILevel >= 0 && opts.MaxAPILevel < math.MaxInt32 {
		placementOpts.MaxAPILevel = ptr.Of(uint32(opts.MaxAPILevel))
	}
	apiServer := placement.NewPlacementService(placementOpts)

	err = concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			sec, serr := secProvider.Handler(ctx)
			if serr != nil {
				return serr
			}
			return raftServer.StartRaft(ctx, sec, nil)
		},
		metricsExporter.Run,
		secProvider.Run,
		apiServer.MonitorLeadership,
		func(ctx context.Context) error {
			var metadataOptions []health.RouterOptions
			if opts.MetadataEnabled {
				metadataOptions = append(metadataOptions, health.NewJSONDataRouterOptions[*placement.PlacementTables]("/placement/state", apiServer.GetPlacementTables))
			}
			healthzServer := health.NewServer(health.Options{
				Log:           log,
				RouterOptions: metadataOptions,
			})
			healthzServer.Ready()
			if healthzErr := healthzServer.Run(ctx, opts.HealthzPort); healthzErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", healthzErr)
			}
			return nil
		},
		func(ctx context.Context) error {
			return apiServer.Run(ctx, strconv.Itoa(opts.PlacementPort))
		},
	).Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Placement service shut down gracefully")
}
