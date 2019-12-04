// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	log "github.com/sirupsen/logrus"
	global_config "github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	daprd "github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/dapr/pkg/version"
)

func main() {
	logLevel := flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
	mode := flag.String("mode", string(modes.StandaloneMode), "Runtime mode for Dapr")
	daprHTTPPort := flag.String("dapr-http-port", fmt.Sprintf("%v", daprd.DefaultDaprHTTPPort), "HTTP port for Dapr to listen on")
	daprGRPCPort := flag.String("dapr-grpc-port", fmt.Sprintf("%v", daprd.DefaultDaprGRPCPort), "gRPC port for Dapr to listen on")
	appPort := flag.String("app-port", "", "The port the application is listening on")
	profilePort := flag.String("profile-port", fmt.Sprintf("%v", daprd.DefaultProfilePort), "The port for the profile server")
	appProtocol := flag.String("protocol", string(daprd.HTTPProtocol), "Protocol for the application: gRPC or http")
	componentsPath := flag.String("components-path", daprd.DefaultComponentsPath, "Path for components directory. Standalone mode only")
	config := flag.String("config", "", "Path to config file, or name of a configuration object")
	daprID := flag.String("dapr-id", "", "A unique ID for Dapr. Used for Service Discovery and state")
	controlPlaneAddress := flag.String("control-plane-address", "", "Address for an Dapr control plane")
	placementServiceAddress := flag.String("placement-address", "", "Address for the Dapr placement service")
	allowedOrigins := flag.String("allowed-origins", daprd.DefaultAllowedOrigins, "Allowed HTTP origins")
	enableProfiling := flag.String("enable-profiling", "false", fmt.Sprintf("Enable profiling. default port is %v", daprd.DefaultComponentsPath))
	runtimeVersion := flag.Bool("version", false, "prints the runtime version")
	maxConcurrency := flag.Int("max-concurrency", -1, "controls the concurrency level when forwarding requests to user code")

	flag.Parse()

	if *runtimeVersion {
		fmt.Println(version.Version())
		os.Exit(0)
	}

	log.Infof("starting Dapr Runtime -- version %s -- commit %s", version.Version(), version.Commit())

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("invalid value for --log-level: %s", *logLevel)
	}

	daprHTTP, err := strconv.Atoi(*daprHTTPPort)
	if err != nil {
		log.Fatalf("error parsing dapr-http-port flag: %s", err)
	}

	daprGRPC, err := strconv.Atoi(*daprGRPCPort)
	if err != nil {
		log.Fatalf("error parsing dapr-grpc-port flag: %s", err)
	}

	profPort, err := strconv.Atoi(*profilePort)
	if err != nil {
		log.Fatalf("error parsing profile-port flag: %s", err)
	}

	applicationPort := 0
	if *appPort != "" {
		applicationPort, err = strconv.Atoi(*appPort)
		if err != nil {
			log.Fatalf("error parsing app-port: %s", err)
		}
	}

	enableProf, err := strconv.ParseBool(*enableProfiling)
	if err != nil {
		log.Fatalf("error parsing enable-profiling: %s", err)
	}

	runtimeConfig := daprd.NewRuntimeConfig(*daprID, *placementServiceAddress, *controlPlaneAddress, *allowedOrigins, *config, *componentsPath,
		*appProtocol, *mode, daprHTTP, daprGRPC, applicationPort, profPort, enableProf, *maxConcurrency)

	var globalConfig *global_config.Configuration

	if *config != "" {
		switch modes.DaprMode(*mode) {
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

	rt := daprd.NewDaprRuntime(runtimeConfig, globalConfig)
	err = rt.Run()
	if err != nil {
		log.Fatalf("error initializing Dapr Runtime: %s", err)
	}

	<-stop
	gracefulShutdownDuration := 5 * time.Second
	log.Info("dapr shutting down. Waiting 5 seconds to finish outstanding operations")
	rt.Stop()
	<-time.After(gracefulShutdownDuration)
}
