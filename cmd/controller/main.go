package main

import (
	"flag"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/controller"
	k8s "github.com/actionscore/actions/pkg/kubernetes"
	"github.com/actionscore/actions/pkg/signals"
	"github.com/actionscore/actions/pkg/version"
)

var (
	logLevel = flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
)

func main() {
	log.Infof("Starting Actions Operator -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()
	kubeClient, actionsClient, err := k8s.Clients()
	if err != nil {
		log.Fatalf("Error building Kubernetes clients: %s", err)
	}

	cfg, err := controller.GetConfigFromEnvironment()
	if err != nil {
		log.Fatalf("Error getting config: %s", err)
	}
	controller.NewController(kubeClient, actionsClient, cfg).Run(ctx)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}

func init() {
	flag.Parse()

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("Log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("Invalid value for --log-level: %s", *logLevel)
	}
}
