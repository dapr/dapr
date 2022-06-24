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

	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/fswatcher"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
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
	flag.StringVar(&credentials.RootCertFilename, "issuer-ca-filename", credentials.RootCertFilename, "Certificate Authority certificate filename")
	flag.StringVar(&credentials.IssuerCertFilename, "issuer-certificate-filename", credentials.IssuerCertFilename, "Issuer certificate filename")
	flag.StringVar(&credentials.IssuerKeyFilename, "issuer-key-filename", credentials.IssuerKeyFilename, "Issuer private key filename")
	trustDomain := flag.String("trust-domain", "localhost", "The CA trust domain")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()
	if err := utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: *kubeconfig,
	}); err != nil {
		log.Fatalf("error set env failed:  %s", err.Error())
	}

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

	runCtx := signals.Context()
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

	go func() {
		// Restart the server when the issuer credentials change
		var restart <-chan time.Time
		for {
			select {
			case <-issuerEvent:
				monitoring.IssuerCertChanged()
				log.Debug("received issuer credentials changed signal")
				// Batch all signals within 2s of each other
				if restart == nil {
					restart = time.After(2 * time.Second)
				}
			case <-restart:
				log.Warn("issuer credentials changed; reloading")
				innerErr := ca.Restart(runCtx, config)
				if innerErr != nil {
					log.Fatalf("failed to restart sentry server: %s", innerErr)
				}
				restart = nil
			}
		}
	}()

	// Start the health server in background
	go func() {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()

		if innerErr := healthzServer.Run(runCtx, healthzPort); innerErr != nil {
			log.Fatalf("failed to start healthz server: %s", innerErr)
		}
	}()

	// Start the server in background
	err = ca.Start(runCtx, config)
	if err != nil {
		log.Fatalf("failed to restart sentry server: %s", err)
	}

	// Watch for changes in the watchDir
	// This also blocks until runCtx is canceled
	fswatcher.Watch(runCtx, watchDir, issuerEvent)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
