// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strconv"

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/fswatcher"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/placement/monitoring"
	"github.com/dapr/dapr/pkg/version"
)

var log = logger.NewLogger("dapr.placement")
var certChainPath string
var tlsEnabled bool

const (
	defaultCredentialsPath   = "/var/run/dapr/credentials"
	defaultHealthzPort       = 8080
	defaultPlacementPort     = 50005
	defaultReplicationFactor = 1000
)

func main() {
	placementPort := flag.Int("port", defaultPlacementPort, "sets the gRPC port for the placement service")
	healthzPort := flag.Int("healthz-port", defaultHealthzPort, "sets the HTTP port for the healthz server")
	replicationFactor := flag.Int("replicationFactor", defaultReplicationFactor, "sets the replication factor for actor distribution on vnodes")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.StringVar(&certChainPath, "certchain", defaultCredentialsPath, "Path to the credentials directory holding the cert chain")
	flag.BoolVar(&tlsEnabled, "tls-enabled", false, "Should TLS be enabled for the placement gRPC server")
	flag.Parse()

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	}

	log.Infof("starting Dapr Placement Service -- version %s -- commit %s", version.Version(), version.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	var certChain *credentials.CertChain
	if tlsEnabled {
		tlsCreds := credentials.NewTLSCredentials(certChainPath)

		log.Info("mTLS enabled, getting tls certificates")
		// try to load certs from disk, if not yet there, start a watch on the local filesystem
		chain, err := credentials.LoadFromDisk(tlsCreds.RootCertPath(), tlsCreds.CertPath(), tlsCreds.KeyPath())
		if err != nil {
			fsevent := make(chan struct{})

			go func() {
				log.Infof("starting watch for certs on filesystem: %s", certChainPath)
				err = fswatcher.Watch(context.Background(), tlsCreds.Path(), fsevent)
				if err != nil {
					log.Fatal("error starting watch on filesystem: %s", err)
				}
			}()

			<-fsevent
			log.Info("certificates detected")

			chain, err = credentials.LoadFromDisk(tlsCreds.RootCertPath(), tlsCreds.CertPath(), tlsCreds.KeyPath())
			if err != nil {
				log.Fatal("failed to load cert chain from disk: %s", err)
			}
		}
		certChain = chain
		log.Info("tls certificates loaded successfully")
	}

	placement.SetReplicationFactor(*replicationFactor)

	p := placement.NewPlacementService()
	go p.Run(strconv.Itoa(*placementPort), certChain)

	log.Infof("placement service started on port %v", *placementPort)

	go func() {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()

		err := healthzServer.Run(context.Background(), *healthzPort)
		if err != nil {
			log.Fatalf("failed to start healthz server: %s", err)
		}
	}()

	<-stop
}
