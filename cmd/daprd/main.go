/*
Copyright 2021 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"fmt"
	"os"

	"go.uber.org/automaxprocs/maxprocs"

	// Register all components
	_ "github.com/dapr/dapr/cmd/daprd/components"

	"github.com/dapr/dapr/cmd/daprd/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	bindingsLoader "github.com/dapr/dapr/pkg/components/bindings"
	configurationLoader "github.com/dapr/dapr/pkg/components/configuration"
	cryptoLoader "github.com/dapr/dapr/pkg/components/crypto"
	lockLoader "github.com/dapr/dapr/pkg/components/lock"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	nrLoader "github.com/dapr/dapr/pkg/components/nameresolution"
	pubsubLoader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstoresLoader "github.com/dapr/dapr/pkg/components/secretstores"
	stateLoader "github.com/dapr/dapr/pkg/components/state"
	workflowsLoader "github.com/dapr/dapr/pkg/components/workflows"

	"github.com/dapr/dapr/pkg/runtime"
	"github.com/dapr/kit/logger"
)

var (
	log        = logger.NewLogger("dapr.runtime")
	logContrib = logger.NewLogger("dapr.contrib")
)

func main() {
	// set GOMAXPROCS
	_, _ = maxprocs.Set()

	opts := options.New(os.Args[1:])

	if opts.RuntimeVersion {
		//nolint:forbidigo
		fmt.Println(buildinfo.Version())
		os.Exit(0)
	}

	if opts.BuildInfo {
		//nolint:forbidigo
		fmt.Printf("Version: %s\nGit Commit: %s\nGit Version: %s\n", buildinfo.Version(), buildinfo.Commit(), buildinfo.GitVersion())
		os.Exit(0)
	}

	if opts.WaitCommand {
		runtime.WaitUntilDaprOutboundReady(opts.DaprHTTPPort)
		os.Exit(0)
	}

	// Apply options to all loggers.
	opts.Logger.SetAppID(opts.AppID)

	if err := logger.ApplyOptionsToLoggers(&opts.Logger); err != nil {
		log.Fatal(err)
	}

	log.Infof("starting Dapr Runtime -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("log level set to: %s", opts.Logger.OutputLevel)

	rt, err := runtime.FromConfig(&runtime.Config{
		AppID:                        opts.AppID,
		PlacementServiceHostAddr:     opts.PlacementServiceHostAddr,
		AllowedOrigins:               opts.AllowedOrigins,
		ResourcesPath:                opts.ResourcesPath,
		ControlPlaneAddress:          opts.ControlPlaneAddress,
		AppProtocol:                  opts.AppProtocol,
		Mode:                         opts.Mode,
		DaprHTTPPort:                 opts.DaprHTTPPort,
		DaprInternalGRPCPort:         opts.DaprInternalGRPCPort,
		DaprAPIGRPCPort:              opts.DaprAPIGRPCPort,
		DaprAPIListenAddresses:       opts.DaprAPIListenAddresses,
		DaprPublicPort:               opts.DaprPublicPort,
		ApplicationPort:              opts.AppPort,
		ProfilePort:                  opts.ProfilePort,
		EnableProfiling:              opts.EnableProfiling,
		AppMaxConcurrency:            opts.AppMaxConcurrency,
		EnableMTLS:                   opts.EnableMTLS,
		SentryAddress:                opts.SentryAddress,
		DaprHTTPMaxRequestSize:       opts.DaprHTTPMaxRequestSize,
		UnixDomainSocket:             opts.UnixDomainSocket,
		DaprHTTPReadBufferSize:       opts.DaprHTTPReadBufferSize,
		DaprGracefulShutdownSeconds:  opts.DaprGracefulShutdownSeconds,
		DisableBuiltinK8sSecretStore: opts.DisableBuiltinK8sSecretStore,
		EnableAppHealthCheck:         opts.EnableAppHealthCheck,
		AppHealthCheckPath:           opts.AppHealthCheckPath,
		AppHealthProbeInterval:       opts.AppHealthProbeInterval,
		AppHealthProbeTimeout:        opts.AppHealthProbeTimeout,
		AppHealthThreshold:           opts.AppHealthThreshold,
		AppChannelAddress:            opts.AppChannelAddress,
		EnableAPILogging:             opts.EnableAPILogging,
		ConfigPath:                   opts.ConfigPath,
		Metrics:                      opts.Metrics,
		AppSSL:                       opts.AppSSL,
		ComponentsPath:               opts.ComponentsPath,
	})
	if err != nil {
		log.Fatal(err)
	}

	secretstoresLoader.DefaultRegistry.Logger = logContrib
	stateLoader.DefaultRegistry.Logger = logContrib
	cryptoLoader.DefaultRegistry.Logger = logContrib
	configurationLoader.DefaultRegistry.Logger = logContrib
	lockLoader.DefaultRegistry.Logger = logContrib
	pubsubLoader.DefaultRegistry.Logger = logContrib
	nrLoader.DefaultRegistry.Logger = logContrib
	bindingsLoader.DefaultRegistry.Logger = logContrib
	workflowsLoader.DefaultRegistry.Logger = logContrib
	httpMiddlewareLoader.DefaultRegistry.Logger = log // Note this uses log on purpose

	stopCh := runtime.ShutdownSignal()

	err = rt.Run(
		runtime.WithSecretStores(secretstoresLoader.DefaultRegistry),
		runtime.WithStates(stateLoader.DefaultRegistry),
		runtime.WithConfigurations(configurationLoader.DefaultRegistry),
		runtime.WithLocks(lockLoader.DefaultRegistry),
		runtime.WithPubSubs(pubsubLoader.DefaultRegistry),
		runtime.WithNameResolutions(nrLoader.DefaultRegistry),
		runtime.WithBindings(bindingsLoader.DefaultRegistry),
		runtime.WithCryptoProviders(cryptoLoader.DefaultRegistry),
		runtime.WithHTTPMiddlewares(httpMiddlewareLoader.DefaultRegistry),
		runtime.WithWorkflowComponents(workflowsLoader.DefaultRegistry),
	)
	if err != nil {
		log.Fatalf("fatal error from runtime: %s", err)
	}

	<-stopCh
	rt.ShutdownWithWait()
}
