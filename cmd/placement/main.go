// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"os"
	"os/signal"

	log "github.com/Sirupsen/logrus"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/version"
)

func main() {
	log.Infof("starting Dapr Placement Service -- version %s -- commit %s", version.Version(), version.Commit())

	logLevel := flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
	port := flag.String("port", "50005", "")
	flag.Parse()

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("invalid value for --log-level: %s", *logLevel)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	p := placement.NewPlacementService()
	go p.Run(*port)

	log.Infof("placement Service started on port %s", *port)
	<-stop
}
