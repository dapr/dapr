// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"time"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	k8s "github.com/dapr/dapr/pkg/kubernetes"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/operator"
	"github.com/dapr/dapr/pkg/operator/monitoring"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
	"k8s.io/klog"
)

var log = logger.NewLogger("dapr.operator")
var config string
var certChainPath string

const (
	defaultCredentialsPath = "/var/run/dapr/credentials"

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config
	defaultDaprSystemConfigName = "daprsystem"
)

func main() {
	log.Infof("starting Dapr Operator -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()

	kubeClient := utils.GetKubeClient()
	kubeConfig := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(kubeConfig)

	if err != nil {
		log.Fatalf("error building Kubernetes clients: %s", err)
	}

	kubeAPI := k8s.NewAPI(kubeClient, daprClient)

	config, err := operator.LoadConfiguration(config, daprClient)
	if err != nil {
		log.Fatal(err)
	}
	config.Credentials = credentials.NewTLSCredentials(certChainPath)

	operator.NewOperator(kubeAPI, config).Run(ctx)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}

func init() {
	// This resets the flags on klog, which will otherwise try to log to the FS.
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	klogFlags.Set("logtostderr", "true")

	loggerOptions := logger.DefaultOptions()
	loggerOptions.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	metricsExporter := metrics.NewExporter(metrics.DefaultMetricNamespace)
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.StringVar(&config, "config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")
	flag.StringVar(&certChainPath, "certchain", defaultCredentialsPath, "Path to the credentials directory holding the cert chain")
	flag.Parse()

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	} else {
		log.Infof("log level set to: %s", loggerOptions.OutputLevel)
	}

	// Initialize dapr metrics exporter
	if err := metricsExporter.Init(); err != nil {
		log.Fatal(err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}
}
