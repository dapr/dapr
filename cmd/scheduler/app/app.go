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
	"time"

	"github.com/dapr/dapr/cmd/scheduler/options"
	"github.com/dapr/dapr/pkg/backoff"
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
		Log:           log,
		Enabled:       opts.Metrics.Enabled(),
		Namespace:     metrics.DefaultMetricNamespace,
		Port:          opts.Metrics.Port(),
		Healthz:       healthz,
		ListenAddress: opts.ListenAddress,
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

	// The controller is long-lived and runs at the top level, not inside a
	// server incarnation: controller-runtime managers cannot be restarted, so
	// the recreate loop below must not tear it down with a failed server. It
	// follows each incarnation's cron via SetCron.
	var ctrl *server.Controller
	if modes.DaprMode(opts.Mode) == modes.KubernetesMode {
		var cerr error
		ctrl, cerr = server.NewController(server.ControllerOptions{
			KubeConfig: opts.KubeConfig,
			Healthz:    healthz,
		})
		if cerr != nil {
			log.Fatalf("Fatal error creating scheduler controller: %v", cerr)
		}
	}

	runners := []concurrency.Runner{
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

			getServer := func() (*server.Server, error) {
				server, serr := server.New(ctx, server.Options{
					Port:                      opts.Port,
					ListenAddress:             opts.ListenAddress,
					OverrideBroadcastHostPort: opts.OverrideBroadcastHostPort,

					Mode:       modes.DaprMode(opts.Mode),
					Security:   secHandler,
					Healthz:    healthz,
					Controller: ctrl,

					KubeConfig:                     opts.KubeConfig,
					EtcdEmbed:                      opts.EtcdEmbed,
					EtcdDataDir:                    opts.EtcdDataDir,
					EtcdName:                       opts.ID,
					EtcdInitialCluster:             opts.EtcdInitialCluster,
					EtcdClientPort:                 opts.EtcdClientPort,
					EtcdClientListenAddress:        opts.EtcdClientListenAddress,
					EtcdSpaceQuota:                 opts.EtcdSpaceQuota,
					EtcdCompactionMode:             opts.EtcdCompactionMode,
					EtcdCompactionRetention:        opts.EtcdCompactionRetention,
					EtcdSnapshotCount:              opts.EtcdSnapshotCount,
					EtcdMaxSnapshots:               opts.EtcdMaxSnapshots,
					EtcdMaxWALs:                    opts.EtcdMaxWALs,
					EtcdBackendBatchLimit:          opts.EtcdBackendBatchLimit,
					EtcdBackendBatchInterval:       opts.EtcdBackendBatchInterval,
					EtcdMaxTxnOps:                  opts.EtcdMaxTxnOps,
					EtcdDefragThresholdMB:          opts.EtcdDefragThresholdMB,
					EtcdInitialElectionTickAdvance: opts.EtcdInitialElectionTickAdvance,
					EtcdMetrics:                    opts.EtcdMetrics,

					EtcdClientEndpoints: opts.EtcdClientEndpoints,
					EtcdClientUsername:  opts.EtcdClientUsername,
					EtcdClientPassword:  opts.EtcdClientPassword,

					Workers: opts.Workers,
				})
				if serr != nil {
					return nil, serr
				}

				return server, nil
			}

			return runServerLoop(ctx, func() (serverRunner, error) {
				return getServer()
			})
		},
	}
	if ctrl != nil {
		runners = append(runners, ctrl.Run)
	}

	err = concurrency.NewRunnerManager(runners...).Run(ctx)
	if err != nil {
		log.Fatalf("Fatal error running scheduler: %v", err)
	}

	log.Info("Scheduler service shut down gracefully")
}

// serverRunner is the subset of *server.Server that runServerLoop drives.
type serverRunner interface {
	Run(ctx context.Context) error
}

const (
	// serverRetryBackoffBase and serverRetryBackoffCap bound the jittered
	// backoff between scheduler server incarnations after a runtime failure.
	serverRetryBackoffBase = 500 * time.Millisecond
	serverRetryBackoffCap  = 10 * time.Second
)

// runServerLoop runs scheduler server incarnations until ctx is cancelled. A
// server that stops with a runtime error (for example the backing store
// becoming unavailable) is recreated after a jittered backoff instead of
// crashing the process: a transient dependency blip must not be fatal to an
// HA fleet, whose members would otherwise all crash-restart together.
// Healthz reports not-ready between incarnations, so orchestration still
// observes the outage. Errors from getServer are configuration failures and
// remain fatal.
func runServerLoop(ctx context.Context, getServer func() (serverRunner, error)) error {
	retryBackoff := backoff.NewJitter(serverRetryBackoffBase, serverRetryBackoffCap)
	for {
		// A shutdown signal can land between iterations: do not construct a
		// fresh server just to tear it down.
		if ctx.Err() != nil {
			return ctx.Err()
		}

		server, err := getServer()
		if err != nil {
			return err
		}

		err = server.Run(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			// A nil return with a live context is a server-requested restart:
			// recreate immediately and reset the failure backoff.
			retryBackoff.Reset()
			continue
		}

		delay := retryBackoff.NextBackOff()
		log.Errorf("Scheduler server failed, recreating in %s: %s", delay, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
