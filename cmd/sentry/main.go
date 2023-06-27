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
	"path/filepath"
	"time"

	"github.com/dapr/dapr/cmd/sentry/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.sentry")

func main() {
	log.Infof("starting sentry certificate authority -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	opts := options.New()

	metricsExporter := metrics.NewExporterWithOptions(metrics.DefaultMetricNamespace, opts.Metrics)

	if err := utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: opts.Kubeconfig,
	}); err != nil {
		log.Fatalf("error set env failed:  %s", err.Error())
	}

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&opts.Logger); err != nil {
		log.Fatal(err)
	}

	log.Infof("log level set to: %s", opts.Logger.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}

	issuerCertPath := filepath.Join(opts.IssuerCredentialsPath, credentials.IssuerCertFilename)
	issuerKeyPath := filepath.Join(opts.IssuerCredentialsPath, credentials.IssuerKeyFilename)
	rootCertPath := filepath.Join(opts.IssuerCredentialsPath, credentials.RootCertFilename)

	config, err := config.FromConfigName(opts.ConfigName)
	if err != nil {
		log.Fatal(err)
	}

	config.IssuerCertPath = issuerCertPath
	config.IssuerKeyPath = issuerKeyPath
	config.RootCertPath = rootCertPath
	config.TrustDomain = opts.TrustDomain
	config.Port = opts.Port
	if opts.TokenAudience != "" {
		config.TokenAudience = &opts.TokenAudience
	}

	var (
		watchDir    = filepath.Dir(config.IssuerCertPath)
		issuerEvent = make(chan struct{})
		mngr        = concurrency.NewRunnerManager()
	)

	// We use runner manager inception here since we want the inner manager to be
	// restarted when the CA server needs to be restarted because of file events.
	// We don't want to restart the healthz server and file watcher on file
	// events (as well as wanting to terminate the program on signals).
	caMngrFactory := func() *concurrency.RunnerManager {
		return concurrency.NewRunnerManager(
			func(ctx context.Context) error {
				return sentry.NewSentryCA().Start(ctx, config)
			},
			func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return nil

				case <-issuerEvent:
					monitoring.IssuerCertChanged()
					log.Debug("received issuer credentials changed signal")

					select {
					case <-ctx.Done():
						return nil
					// Batch all signals within 2s of each other
					case <-time.After(2 * time.Second):
						log.Warn("issuer credentials changed; reloading")
						return nil
					}
				}
			},
		)
	}

	// CA Server
	mngr.Add(func(ctx context.Context) error {
		for {
			if err := caMngrFactory().Run(ctx); err != nil {
				return err
			}
			// Catch outer context cancellation to exit.
			select {
			case <-ctx.Done():
				return nil
			default:
			}
		}
	})

	// Watch for changes in the watchDir
	mngr.Add(func(ctx context.Context) error {
		log.Infof("starting watch on filesystem directory: %s", watchDir)
		return fswatcher.Watch(ctx, watchDir, issuerEvent)
	})

	// Healthz server
	mngr.Add(func(ctx context.Context) error {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()
		if err := healthzServer.Run(ctx, opts.HealthzPort); err != nil {
			return fmt.Errorf("failed to start healthz server: %s", err)
		}
		return nil
	})

	// Run the runner manager.
	if err := mngr.Run(signals.Context()); err != nil {
		log.Fatal(err)
	}
	log.Info("sentry shut down gracefully")
}
