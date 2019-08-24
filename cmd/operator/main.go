package main

import (
	"flag"
	"time"

	log "github.com/Sirupsen/logrus"
	k8s "github.com/actionscore/actions/pkg/kubernetes"
	"github.com/actionscore/actions/pkg/operator"
	"github.com/actionscore/actions/pkg/signals"
	"github.com/actionscore/actions/pkg/version"
)

var (
	logLevel = flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
)

func main() {
	log.Infof("starting Actions Operator -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()
	kubeClient, actionsClient, err := k8s.Clients()
	if err != nil {
		log.Fatalf("error building Kubernetes clients: %s", err)
	}
	operator.NewOperator(kubeClient, actionsClient).Run(ctx)

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
