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
	"os"

	"github.com/dapr/dapr/cmd/placement/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/healthz"
	healthzserver "github.com/dapr/dapr/pkg/healthz/server"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/placement/raft"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/signals"
)

var log = logger.NewLogger("dapr.placement")

func Run() {
	opts, err := options.New(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	// Apply options to all loggers.
	if e := logger.ApplyOptionsToLoggers(&opts.Logger); e != nil {
		log.Fatal(e)
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

	if e := monitoring.InitMetrics(); e != nil {
		log.Fatal(e)
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

	raftOptions := raft.Options{
		ID:                opts.RaftID,
		InMem:             opts.RaftInMemEnabled,
		Peers:             opts.RaftPeers,
		LogStorePath:      opts.RaftLogStorePath,
		ReplicationFactor: int64(opts.ReplicationFactor),
		// TODO: fix types
		//nolint:gosec
		MinAPILevel: uint32(opts.MinAPILevel),
		//nolint:gosec
		MaxAPILevel: uint32(opts.MaxAPILevel),
		Healthz:     healthz,
		Security:    secProvider,
	}

	placementOpts := placement.ServiceOpts{
		Port:               opts.PlacementPort,
		Raft:               raftOptions,
		SecProvider:        secProvider,
		Healthz:            healthz,
		KeepAliveTime:      opts.KeepAliveTime,
		KeepAliveTimeout:   opts.KeepAliveTimeout,
		DisseminateTimeout: opts.DisseminateTimeout,
		ListenAddress:      opts.PlacementListenAddress,
	}
	placementOpts.SetMinAPILevel(opts.MinAPILevel)
	placementOpts.SetMaxAPILevel(opts.MaxAPILevel)

	placementService, err := placement.New(placementOpts)
	if err != nil {
		log.Fatal("failed to create placement service: ", err)
	}

	var healthzHandlers []healthzserver.Handler
	if opts.MetadataEnabled {
		healthzHandlers = append(healthzHandlers, healthzserver.Handler{
			Path: "/placement/state",
			Getter: func() ([]byte, error) {
				var tables *placement.PlacementTables
				tables, err = placementService.GetPlacementTables()
				if err != nil {
					return nil, err
				}
				return json.Marshal(tables)
			},
		})
	}

	healthSrv := healthzserver.New(healthzserver.Options{
		Log:      log,
		Port:     opts.HealthzPort,
		Healthz:  healthz,
		Handlers: healthzHandlers,
	})

	err = concurrency.NewRunnerManager(
		secProvider.Run,
		metricsExporter.Start,
		healthSrv.Start,
		placementService.Run,
	).Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Placement service shut down gracefully")
}
