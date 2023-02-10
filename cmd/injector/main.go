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
	controllerruntime "sigs.k8s.io/controller-runtime"

	"github.com/dapr/dapr/pkg/buildinfo"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/injector"
	"github.com/dapr/dapr/pkg/injector/monitoring"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/logger"
)

var (
	log         = logger.NewLogger("dapr.injector")
	healthzPort int
)

func main() {
	log.Infof("starting Dapr Sidecar Injector -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	ctx := signals.Context()
	cfg, err := injector.GetConfig()
	if err != nil {
		log.Fatalf("error getting config: %s", err)
	}

	kubeClient := utils.GetKubeClient()
	conf := controllerruntime.GetConfigOrDie()
	daprClient, _ := scheme.NewForConfig(conf)

	healthzServer := health.NewServer(log)
	go func() {
		healthzErr := healthzServer.Run(ctx, healthzPort)
		if healthzErr != nil {
			log.Fatalf("failed to start healthz server: %s", healthzErr)
		}
	}()

	uids, err := injector.AllowedControllersServiceAccountUID(ctx, cfg, kubeClient)
	if err != nil {
		log.Fatalf("failed to get authentication uids from services accounts: %s", err)
	}

	inj, err := injector.NewInjector(uids, cfg, daprClient, kubeClient)
	if err != nil {
		log.Fatalf("could not create Injector, err: %s", err)
	}
	// Blocking call
	inj.Run(ctx, func() {
		healthzServer.Ready()
	})

	log.Infof("Dapr sidecar injector shut down")
}

func init() {
	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.IntVar(&healthzPort, "healthz-port", 8080, "The port used for health checks")

	flag.StringVar(&credentials.RootCertFilename, "issuer-ca-secret-key", credentials.RootCertFilename, "Certificate Authority certificate secret key")
	flag.StringVar(&credentials.IssuerCertFilename, "issuer-certificate-secret-key", credentials.IssuerCertFilename, "Issuer certificate secret key")
	flag.StringVar(&credentials.IssuerKeyFilename, "issuer-key-secret-key", credentials.IssuerKeyFilename, "Issuer private key secret key")

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
