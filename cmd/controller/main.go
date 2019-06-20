package main

import (
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/golang/glog"
	"github.com/actionscore/actions/pkg/controller"
	k8s "github.com/actionscore/actions/pkg/kubernetes"
	"github.com/actionscore/actions/pkg/signals"
)

func main() {
	log.Info("Starting Actions Controller")

	ctx := signals.Context()
	kubeClient, actionsClient, err := k8s.Clients()
	if err != nil {
		log.Fatalf("Error building Kubernetes clients: %s", err)
	}

	controller.NewController(kubeClient, actionsClient).Run(ctx)

	shutdownDuration := 5 * time.Second
	glog.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}
