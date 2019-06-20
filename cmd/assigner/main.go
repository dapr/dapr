package main

import (
	"flag"
	"os"
	"os/signal"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/assigner"
)

func main() {
	port := flag.String("port", "50005", "")
	flag.Parse()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	a := assigner.NewAssigner()
	go a.Run(*port)

	log.Infof("Assigner started on port %s", *port)
	<-stop
}
