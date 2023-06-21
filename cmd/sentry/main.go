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
	"flag"
	"fmt"
	"path/filepath"
	"time"

	"k8s.io/client-go/util/homedir"

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

//nolint:gosec
const (
	defaultCredentialsPath = "/var/run/dapr/credentials"
	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	defaultHealthzPort = 8080
)

func main() {
	configName := flag.String("config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")
	credsPath := flag.String("issuer-credentials", defaultCredentialsPath, "Path to the credentials directory holding the issuer data")
	flag.StringVar(&credentials.RootCertFilename, "issuer-ca-filename", credentials.RootCertFilename, "Certificate Authority certificate filename")
	flag.StringVar(&credentials.IssuerCertFilename, "issuer-certificate-filename", credentials.IssuerCertFilename, "Issuer certificate filename")
	flag.StringVar(&credentials.IssuerKeyFilename, "issuer-key-filename", credentials.IssuerKeyFilename, "Issuer private key filename")
	trustDomain := flag.String("trust-domain", "localhost", "The CA trust domain")
	tokenAudience := flag.String("token-audience", "", "Expected audience for tokens; multiple values can be separated by a comma")
	port := flag.Int("port", config.DefaultPort, "The port for the sentry server to listen on")
	healthzPort := flag.Int("healthz-port", defaultHealthzPort, "The port for the healthz server to listen on")

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

	log.Infof("starting sentry certificate authority -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
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

	config, err := config.FromConfigName(*configName)
	if err != nil {
		log.Fatal(err)
	}

	config.IssuerCertPath = issuerCertPath
	config.IssuerKeyPath = issuerKeyPath
	config.RootCertPath = rootCertPath
	config.TrustDomain = *trustDomain
	config.Port = *port
	if *tokenAudience != "" {
		config.TokenAudience = tokenAudience
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
		if err := healthzServer.Run(ctx, *healthzPort); err != nil {
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
