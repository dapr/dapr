package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	log "github.com/Sirupsen/logrus"
	global_config "github.com/actionscore/actions/pkg/config"
	"github.com/actionscore/actions/pkg/modes"
	actionsrt "github.com/actionscore/actions/pkg/runtime"
	"github.com/actionscore/actions/pkg/version"
)

func main() {
	log.Infof("starting Actions Runtime -- version %s -- commit %s", version.Version(), version.Commit())

	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Actions")
	actionHTTPPort := flag.String("actions-http-port", fmt.Sprintf("%v", actionsrt.DefaultActionsHTTPPort), "HTTP port for Actions to listen on")
	actionGRPCPort := flag.String("actions-grpc-port", fmt.Sprintf("%v", actionsrt.DefaultActionsGRPCPort), "gRPC port for Actions to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	appProtocol := flag.String("protocol", string(actionsrt.HTTPProtocol), "Protocol for the application: gRPC or http")
	componentsPath := flag.String("components-path", actionsrt.DefaultComponentsPath, "Path for components directory. Standalone mode only")
	config := flag.String("config", "", "Path to config file, or name of a configuration object")
	actionsID := flag.String("actions-id", "", "A unique ID for Actions. Used for Service Discovery and state")
	controlPlaneAddress := flag.String("control-plane-address", "", "Address for an Actions control plane")
	placementServiceAddresss := flag.String("placement-address", "", "Address for the Actions placement service")
	allowedOrigins := flag.String("allowed-origins", actionsrt.DefaultAllowedOrigins, "Allowed HTTP origins")

	flag.Parse()

	actionHTTP, err := strconv.Atoi(*actionHTTPPort)
	if err != nil {
		log.Fatalf("error parsing actions-http-port flag: %s", err)
	}

	actionGRPC, err := strconv.Atoi(*actionGRPCPort)
	if err != nil {
		log.Fatalf("error parsing actions-grpc-port flag: %s", err)
	}

	applicationPort := 0
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			log.Fatalf("error parsing app-port: %s", err)
		}
	}

	runtimeConfig := actionsrt.NewRuntimeConfig(*actionsID, *placementServiceAddresss, *controlPlaneAddress, *allowedOrigins, *config, *componentsPath,
		*appProtocol, *mode, actionHTTP, actionGRPC, applicationPort)

	var globalConfig *global_config.Configuration

	if *config != "" {
		switch modes.ActionsMode(*mode) {
		case modes.KubernetesMode:
			globalConfig, err = global_config.LoadKubernetesConfiguration(*config, *controlPlaneAddress)
		case modes.StandaloneMode:
			globalConfig, err = global_config.LoadStandaloneConfiguration(*config)
		}
	} else {
		globalConfig = global_config.LoadDefaultConfiguration()
	}

	if err != nil {
		log.Warnf("error loading config: %s. loading default config", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, os.Interrupt)

	rt := actionsrt.NewActionsRuntime(runtimeConfig, globalConfig)
	err = rt.Run()
	if err != nil {
		log.Fatalf("error initializing Actions Runtime: %s", err)
	}

	<-stop
	gracefulShutdownDuration := 5 * time.Second
	log.Info("actions shutting down. Waiting 5 seconds to finish outstanding operations")
	rt.Stop()
	<-time.After(gracefulShutdownDuration)
}
