// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"os"
	"os/signal"

	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/placement"
	"github.com/dapr/dapr/pkg/version"
)

var log = logger.NewLogger("dapr.placement")

func main() {
	port := flag.String("port", "50005", "")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	}

	log.Infof("starting Dapr Placement Service -- version %s -- commit %s", version.Version(), version.Commit())
	log.Infof("log level set to: %s", loggerOptions.OutputLevel)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	p := placement.NewPlacementService()
	go p.Run(*port)

	log.Infof("placement Service started on port %s", *port)
	<-stop
}
