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
	"encoding/json"
	"math"
	"os"

	"github.com/dapr/dapr/cmd/placement/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/healthz/server"
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

	healthz := healthz.New()
	metricsExporter := metrics.New(metrics.Options{
		Log:       log,
		Enabled:   opts.Metrics.Enabled(),
		Namespace: metrics.DefaultMetricNamespace,
		Port:      opts.Metrics.Port(),
		Healthz:   healthz,
	})

	err := monitoring.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	ctx := signals.Context()
	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           opts.SentryAddress,
		ControlPlaneTrustDomain: opts.TrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        &opts.TrustAnchorsFile,
		AppID:                   "dapr-placement",
		MTLSEnabled:             opts.TLSEnabled,
		Mode:                    modes.DaprMode(opts.Mode),
		Healthz:                 healthz,
	})
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
		Healthz:           healthz,
		Security:          secProvider,
	})
	if raftServer == nil {
		log.Fatal("Failed to create raft server.")
	}

	placementOpts := placement.PlacementServiceOpts{
		Port:        opts.PlacementPort,
		RaftNode:    raftServer,
		SecProvider: secProvider,
		Healthz:     healthz,
	}
	if opts.MinAPILevel >= 0 && opts.MinAPILevel < math.MaxInt32 {
		placementOpts.MinAPILevel = uint32(opts.MinAPILevel)
	}
	if opts.MaxAPILevel >= 0 && opts.MaxAPILevel < math.MaxInt32 {
		placementOpts.MaxAPILevel = ptr.Of(uint32(opts.MaxAPILevel))
	}
	apiServer := placement.NewPlacementService(placementOpts)
	var healthzHandlers []server.Handler
	if opts.MetadataEnabled {
		healthzHandlers = append(healthzHandlers, server.Handler{
			Path: "/placement/state",
			Getter: func() ([]byte, error) {
				var tables *placement.PlacementTables
				tables, err = apiServer.GetPlacementTables()
				if err != nil {
					return nil, err
				}
				return json.Marshal(tables)
			},
		})
	}

	err = concurrency.NewRunnerManager(
		secProvider.Run,
		raftServer.StartRaft,
		metricsExporter.Start,
		server.New(server.Options{
			Log:      log,
			Port:     opts.HealthzPort,
			Healthz:  healthz,
			Handlers: healthzHandlers,
		}).Start,
		apiServer.MonitorLeadership,
		apiServer.Start,
	).Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Placement service shut down gracefully")
}
