/*
Copyright 2024 The Dapr Authors
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
	"os"

	"github.com/dapr/dapr/cmd/scheduler/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/healthz"
	healthzserver "github.com/dapr/dapr/pkg/healthz/server"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/scheduler/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/signals"
)

var log = logger.NewLogger("dapr.scheduler")

const appID = "dapr-scheduler"

func Run() {
	opts, err := options.New(os.Args[1:])
	if err != nil {
		log.Fatal(err)
	}

	// Apply options to all loggers.
	if lerr := logger.ApplyOptionsToLoggers(&opts.Logger); lerr != nil {
		log.Fatal(lerr)
	}

	log.Infof("Starting Dapr Scheduler Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("Log level set to: %s", opts.Logger.OutputLevel)

	healthz := healthz.New()

	metricsExporter := metrics.New(metrics.Options{
		Log:       log,
		Enabled:   opts.Metrics.Enabled(),
		Namespace: metrics.DefaultMetricNamespace,
		Port:      opts.Metrics.Port(),
		Healthz:   healthz,
	})

	if merr := monitoring.InitMetrics(); merr != nil {
		log.Fatal(merr)
	}

	ctx := signals.Context()
	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           opts.SentryAddress,
		ControlPlaneTrustDomain: opts.TrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        opts.TrustAnchorsFile,
		AppID:                   appID,
		MTLSEnabled:             opts.TLSEnabled || opts.Mode == string(modes.KubernetesMode),
		Mode:                    modes.DaprMode(opts.Mode),
		Healthz:                 healthz,
		WriteIdentityToFile:     &opts.IdentityDirectoryWrite,
	})
	if err != nil {
		log.Fatal(err)
	}

	err = concurrency.NewRunnerManager(
		healthzserver.New(healthzserver.Options{
			Log:     log,
			Port:    opts.HealthzPort,
			Healthz: healthz,
		}).Start,
		metricsExporter.Start,
		secProvider.Run,
		func(ctx context.Context) error {
			secHandler, serr := secProvider.Handler(ctx)
			if serr != nil {
				return serr
			}

			server, serr := server.New(server.Options{
				Port:                      opts.Port,
				ListenAddress:             opts.ListenAddress,
				OverrideBroadcastHostPort: opts.OverrideBroadcastHostPort,

				Mode:     modes.DaprMode(opts.Mode),
				Security: secHandler,
				Healthz:  healthz,

				KubeConfig:                     opts.KubeConfig,
				EtcdEmbed:                      opts.EtcdEmbed,
				EtcdDataDir:                    opts.EtcdDataDir,
				EtcdName:                       opts.ID,
				EtcdInitialCluster:             opts.EtcdInitialCluster,
				EtcdClientPort:                 opts.EtcdClientPort,
				EtcdSpaceQuota:                 opts.EtcdSpaceQuota,
				EtcdCompactionMode:             opts.EtcdCompactionMode,
				EtcdCompactionRetention:        opts.EtcdCompactionRetention,
				EtcdSnapshotCount:              opts.EtcdSnapshotCount,
				EtcdMaxSnapshots:               opts.EtcdMaxSnapshots,
				EtcdMaxWALs:                    opts.EtcdMaxWALs,
				EtcdBackendBatchLimit:          opts.EtcdBackendBatchLimit,
				EtcdBackendBatchInterval:       opts.EtcdBackendBatchInterval,
				EtcdDefragThresholdMB:          opts.EtcdDefragThresholdMB,
				EtcdInitialElectionTickAdvance: opts.EtcdInitialElectionTickAdvance,
				EtcdMetrics:                    opts.EtcdMetrics,

				EtcdClientEndpoints: opts.EtcdClientEndpoints,
				EtcdClientUsername:  opts.EtcdClientUsername,
				EtcdClientPassword:  opts.EtcdClientPassword,
			})
			if serr != nil {
				return serr
			}

			return server.Run(ctx)
		},
	).Run(ctx)
	if err != nil {
		log.Fatalf("Fatal error running scheduler: %v", err)
	}

	log.Info("Scheduler service shut down gracefully")
}
