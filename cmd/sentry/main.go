// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	log "github.com/sirupsen/logrus"
)

func main() {
	logLevel := flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
	configName := flag.String("config", "default", "Path to config file, or name of a configuration object")
	credsPath := flag.String("issuer-credentials", "/var/run/dapr/credentials", "Path to the credentials directory holding the issuer data")

	flag.Parse()

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("invalid value for --log-level: %s", *logLevel)
	}

	log.Infof("starting Sentry Certificate Authority -- version %s -- commit %s", version.Version(), version.Commit())

	issuerCertPath := filepath.Join(*credsPath, config.IssuerCertFilename)
	issuerKeyPath := filepath.Join(*credsPath, config.IssuerKeyFilename)
	rootCertPath := filepath.Join(*credsPath, config.RootCertFilename)

	if _, err = os.Stat(issuerCertPath); err != nil {
		log.Fatalf("can't find issuer cert at %s", issuerCertPath)
	}

	if _, err = os.Stat(issuerKeyPath); err != nil {
		log.Fatalf("can't find issuer key at %s", issuerKeyPath)
	}

	if _, err = os.Stat(rootCertPath); err != nil {
		log.Fatalf("can't find trust anchors at %s", rootCertPath)
	}

	ctx := signals.Context()
	config, err := config.FromConfigName(*configName)
	if err != nil {
		log.Error(err)
	}
	config.IssuerCertPath = issuerCertPath
	config.IssuerKeyPath = issuerKeyPath
	config.RootCertPath = rootCertPath

	sentry.NewSentryCA().Run(ctx, config)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
