// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/watcher"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	log "github.com/sirupsen/logrus"
)

func main() {
	logLevel := flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
	configName := flag.String("config", "default", "Path to config file, or name of a configuration object")
	credsPath := flag.String("issuer-credentials", "/var/run/dapr/credentials", "Path to the credentials directory holding the issuer data")
	trustDomain := flag.String("trust-domain", "localhost", "The CA trust domain")

	flag.Parse()

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("invalid value for --log-level: %s", *logLevel)
	}

	log.Infof("starting sentry certificate authority -- version %s -- commit %s", version.Version(), version.Commit())

	issuerCertPath := filepath.Join(*credsPath, config.IssuerCertFilename)
	issuerKeyPath := filepath.Join(*credsPath, config.IssuerKeyFilename)
	rootCertPath := filepath.Join(*credsPath, config.RootCertFilename)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	ctx := signals.Context()
	config, err := config.FromConfigName(*configName)
	if err != nil {
		log.Warning(err)
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

	go watcher.StartIssuerWatcher(ctx, watchDir, issuerEvent)
	go func() {
		for range issuerEvent {
			log.Warning("issuer credentials changed. reloading")
			ca.Restart(ctx, config)
		}
	}()

	<-stop
	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
