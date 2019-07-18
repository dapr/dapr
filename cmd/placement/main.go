package main

import (
	"flag"
	"os"
	"os/signal"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/placement"
	"github.com/actionscore/actions/pkg/version"
)

func main() {
	log.Infof("starting Actions Placement Service -- version %s -- commit %s", version.Version(), version.Commit())

	port := flag.String("port", "50005", "")
	flag.Parse()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	p := placement.NewPlacementService()
	go p.Run(*port)

	log.Infof("placement Service started on port %s", *port)
	<-stop
}
