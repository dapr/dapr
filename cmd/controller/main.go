package main

import (
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/controller"
	k8s "github.com/actionscore/actions/pkg/kubernetes"
	"github.com/actionscore/actions/pkg/signals"
	"github.com/actionscore/actions/pkg/version"
)

func main() {
	log.Infof("Starting Actions Operator -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()
	kubeClient, actionsClient, err := k8s.Clients()
	if err != nil {
		log.Fatalf("Error building Kubernetes clients: %s", err)
	}

	controller.NewController(kubeClient, actionsClient).Run(ctx)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
