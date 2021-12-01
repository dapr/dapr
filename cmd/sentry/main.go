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
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dapr/kit/logger"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/fswatcher"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
)

var log = logger.NewLogger("dapr.sentry")

const (
	defaultCredentialsPath = "/var/run/dapr/credentials"
	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	healthzPort = 8080
)

func main() {
	configName := flag.String("config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")
	credsPath := flag.String("issuer-credentials", defaultCredentialsPath, "Path to the credentials directory holding the issuer data")
	trustDomain := flag.String("trust-domain", "localhost", "The CA trust domain")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	}

	log.Infof("starting sentry certificate authority -- version %s -- commit %s", version.Version(), version.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}

	issuerCertPath := filepath.Join(*credsPath, credentials.IssuerCertFilename)
	issuerKeyPath := filepath.Join(*credsPath, credentials.IssuerKeyFilename)
	rootCertPath := filepath.Join(*credsPath, credentials.RootCertFilename)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	ctx := signals.Context()
	config, err := config.FromConfigName(*configName)
	if err != nil {
		log.Warn(err)
	}
	config.IssuerCertPath = issuerCertPath
	config.IssuerKeyPath = issuerKeyPath
	config.RootCertPath = rootCertPath
	config.TrustDomain = *trustDomain

	watchDir := filepath.Dir(config.IssuerCertPath)

	ca := sentry.NewSentryCA()

	log.Infof("starting watch on filesystem directory: %s", watchDir)
	issuerEvent := make(chan struct{})
	ready := make(chan bool)

	go ca.Run(ctx, config, ready)

	<-ready

	go fswatcher.Watch(ctx, watchDir, issuerEvent)

	go func() {
		for range issuerEvent {
			monitoring.IssuerCertChanged()
			log.Warn("issuer credentials changed. reloading")
			ca.Restart(ctx, config)
		}
	}()

	go func() {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()

		err := healthzServer.Run(ctx, healthzPort)
		if err != nil {
			log.Fatalf("failed to start healthz server: %s", err)
		}
	}()

	<-stop
	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
