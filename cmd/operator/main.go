// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package main

import (
	"flag"
	"time"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	k8s "github.com/dapr/dapr/pkg/kubernetes"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/operator"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/dapr/pkg/version"
	"github.com/dapr/dapr/utils"
	"k8s.io/klog"
)

var log = logger.NewLogger("dapr.operator")

func main() {
	log.Infof("starting Dapr Operator -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()

	kubeClient := utils.GetKubeClient()

	config := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(config)

	if err != nil {
		log.Fatalf("error building Kubernetes clients: %s", err)
	}

	kubeAPI := k8s.NewAPI(kubeClient, daprClient)

	operator.NewOperator(kubeAPI).Run(ctx)

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

	metricsExporter := metrics.NewDaprMetricExporter()
	metricsExporter.Options().AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&loggerOptions); err != nil {
		log.Fatal(err)
	} else {
		log.Infof("log level set to: %s", loggerOptions.OutputLevel)
	}

	// Initialize dapr metrics exporter
	metricsExporter.Init(metrics.DefaultMetricNamespace)
	metricsExporter.StartMetricServer()
}
