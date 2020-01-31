// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"time"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/injector"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
	log "github.com/sirupsen/logrus"
)

var (
	logLevel = flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
)

func main() {
	log.Infof("starting Dapr Sidecar Injector -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()
	cfg, err := injector.GetConfigFromEnvironment()
	if err != nil {
		log.Fatalf("error getting config: %s", err)
	}

	kubeClient := utils.GetKubeClient()
	conf := utils.GetConfig()
	daprClient, _ := scheme.NewForConfig(conf)

	injector.NewInjector(cfg, daprClient, kubeClient).Run(ctx)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}

func init() {
	flag.Parse()

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("invalid value for --log-level: %s", *logLevel)
	}
}
