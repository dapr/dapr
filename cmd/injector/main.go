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

	"github.com/dapr/dapr/cmd/injector/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/concurrency"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/injector/service"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.injector")

func main() {
	opts := options.New()

	// Apply options to all loggers
	err := logger.ApplyOptionsToLoggers(&opts.Logger)
	if err != nil {
		log.Fatal(err)
	}

	log.Infof("Starting Dapr Sidecar Injector -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("Log level set to: %s", opts.Logger.OutputLevel)

	metricsExporter := metrics.NewExporterWithOptions(log, metrics.DefaultMetricNamespace, opts.Metrics)

	err = utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: opts.Kubeconfig,
	})
	if err != nil {
		log.Fatalf("Error set env: %v", err)
	}

	// Initialize dapr metrics exporter
	err = metricsExporter.Init()
	if err != nil {
		log.Fatal(err)
	}

	// Initialize injector service metrics
	err = service.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	ctx := signals.Context()
	cfg, err := service.GetConfig()
	if err != nil {
		log.Fatalf("Error getting config: %v", err)
	}

	kubeClient := utils.GetKubeClient()
	conf := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(conf)
	if err != nil {
		log.Fatalf("Error creating Dapr client: %v", err)
	}
	uids, err := service.AllowedControllersServiceAccountUID(ctx, cfg, kubeClient)
	if err != nil {
		log.Fatalf("Failed to get authentication uids from services accounts: %s", err)
	}

	inj, err := service.NewInjector(uids, cfg, daprClient, kubeClient)
	if err != nil {
		log.Fatalf("Error creating injector: %v", err)
	}

	healthzServer := health.NewServer(log)
	mngr := concurrency.NewRunnerManager(
		inj.Run,
		func(ctx context.Context) error {
			readyErr := inj.Ready(ctx)
			if readyErr != nil {
				return readyErr
			}
			healthzServer.Ready()
			<-ctx.Done()
			return nil
		},
		func(ctx context.Context) error {
			healhtzErr := healthzServer.Run(ctx, opts.HealthzPort)
			if healhtzErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", healhtzErr)
			}
			return nil
		},
	)

	err = mngr.Run(ctx)
	if err != nil {
		log.Fatalf("Error running injector: %v", err)
	}

	log.Infof("Dapr sidecar injector shut down gracefully")
}
