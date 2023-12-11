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

package main

import (
	"context"
	"fmt"

	"github.com/dapr/dapr/cmd/scheduler/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/scheduler/server"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/signals"
)

var log = logger.NewLogger("dapr.scheduler")

func main() {
	opts := options.New()

	// Apply options to all loggers.
	if err := logger.ApplyOptionsToLoggers(&opts.Logger); err != nil {
		log.Fatal(err)
	}

	log.Infof("Starting Dapr Scheduler Service -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("Log level set to: %s", opts.Logger.OutputLevel)

	metricsExporter := metrics.NewExporterWithOptions(log, metrics.DefaultMetricNamespace, opts.Metrics)

	err := monitoring.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	ctx := signals.Context()
	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           opts.SentryAddress,
		ControlPlaneTrustDomain: opts.TrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        opts.TrustAnchorsFile,
		AppID:                   "dapr-scheduler",
		MTLSEnabled:             opts.TLSEnabled,
		Mode:                    modes.DaprMode(opts.Mode),
	})
	if err != nil {
		log.Fatal(err)
	}

	err = concurrency.NewRunnerManager(
		metricsExporter.Run,
		secProvider.Run,
		func(ctx context.Context) error {
			secHandler, serr := secProvider.Handler(ctx)
			if serr != nil {
				return serr
			}
			return server.Start(ctx, server.SchedulerServiceOpts{}, secHandler)
		},
		func(ctx context.Context) error {
			healthzServer := health.NewServer(log)
			healthzServer.Ready()
			if healthzErr := healthzServer.Run(ctx, opts.HealthzPort); healthzErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", healthzErr)
			}
			return nil
		},
	).Run(ctx)
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Scheduler service shut down gracefully")
}
