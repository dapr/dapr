// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"time"

	"github.com/dapr/kit/logger"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/injector"
	"github.com/dapr/dapr/pkg/injector/monitoring"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
)

var log = logger.NewLogger("dapr.injector")

const (
	healthzPort = 8080
)

func main() {
	logger.DaprVersion = version.Version()
	log.Infof("starting Dapr Sidecar Injector -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()
	cfg, err := injector.GetConfig()
	if err != nil {
		log.Fatalf("error getting config: %s", err)
	}

	kubeClient := utils.GetKubeClient()
	conf := utils.GetConfig()
	daprClient, _ := scheme.NewForConfig(conf)

	go func() {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()

		healthzErr := healthzServer.Run(ctx, healthzPort)
		if healthzErr != nil {
			log.Fatalf("failed to start healthz server: %s", healthzErr)
		}
	}()

	uids, err := injector.AllowedControllersServiceAccountUID(ctx, kubeClient)
	if err != nil {
		log.Fatalf("failed to get authentication uids from services accounts: %s", err)
	}

	injector.NewInjector(uids, cfg, daprClient, kubeClient).Run(ctx)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}

func init() {
	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	} else {
		log.Infof("log level set to: %s", loggerOptions.OutputLevel)
	}

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	// Initialize injector service metrics
	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}
}
