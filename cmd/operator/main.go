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
	"flag"
	"strings"
	"time"

	"k8s.io/klog"

	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/operator"
	"github.com/dapr/dapr/pkg/operator/monitoring"
	"github.com/dapr/dapr/pkg/signals"
	"github.com/dapr/kit/logger"
)

var (
	log                                = logger.NewLogger("dapr.operator")
	config                             string
	certChainPath                      string
	watchInterval                      string
	maxPodRestartsPerMinute            int
	disableLeaderElection              bool
	disableServiceReconciler           bool
	watchNamespace                     string
	enableArgoRolloutServiceReconciler bool
	watchdogCanPatchPodLabels          bool
)

//nolint:gosec
const (
	// defaultCredentialsPath is the default path for the credentials (the K8s mountpoint by default).
	defaultCredentialsPath = "/var/run/dapr/credentials"

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	// defaultWatchInterval is the default value for watch-interval, in seconds (note this is a string as `once` is an acceptable value too).
	defaultWatchInterval = "0"

	// defaultMaxPodRestartsPerMinute is the default value for max-pod-restarts-per-minute.
	defaultMaxPodRestartsPerMinute = 20
)

func main() {
	log.Infof("starting Dapr Operator -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())

	operatorOpts := operator.Options{
		Config:                              config,
		CertChainPath:                       certChainPath,
		LeaderElection:                      !disableLeaderElection,
		WatchdogEnabled:                     false,
		WatchdogInterval:                    0,
		WatchdogMaxRestartsPerMin:           maxPodRestartsPerMinute,
		WatchNamespace:                      watchNamespace,
		ServiceReconcilerEnabled:            !disableServiceReconciler,
		ArgoRolloutServiceReconcilerEnabled: enableArgoRolloutServiceReconciler,
		WatchdogCanPatchPodLabels:           watchdogCanPatchPodLabels,
	}

	wilc := strings.ToLower(watchInterval)
	switch wilc {
	case "0", "false", "f", "no", "off":
		// Disabled - do nothing
	default:
		operatorOpts.WatchdogEnabled = true
		if wilc != "once" {
			dur, err := time.ParseDuration(watchInterval)
			if err != nil {
				log.Fatalf("invalid value for watch-interval: %v", err)
			}
			if dur < time.Second {
				log.Fatalf("invalid watch-interval value: if not '0' or 'once', must be at least 1s")
			}
			operatorOpts.WatchdogInterval = dur
		}
	}

	ctx := signals.Context()

	op, err := operator.NewOperator(ctx, operatorOpts)
	if err != nil {
		log.Fatalf("error creating operator: %v", err)
	}

	err = op.Run(ctx)
	if err != nil {
		log.Fatalf("error running operator: %v", err)
	}
	log.Info("operator shut down gracefully")
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

	flag.StringVar(&credentials.RootCertFilename, "issuer-ca-filename", credentials.RootCertFilename, "Certificate Authority certificate filename")
	flag.StringVar(&credentials.IssuerCertFilename, "issuer-certificate-filename", credentials.IssuerCertFilename, "Issuer certificate filename")
	flag.StringVar(&credentials.IssuerKeyFilename, "issuer-key-filename", credentials.IssuerKeyFilename, "Issuer private key filename")

	flag.StringVar(&watchInterval, "watch-interval", defaultWatchInterval, "Interval for polling pods' state, e.g. '2m'. Set to '0' to disable, or 'once' to only run once when the operator starts")
	flag.IntVar(&maxPodRestartsPerMinute, "max-pod-restarts-per-minute", defaultMaxPodRestartsPerMinute, "Maximum number of pods in an invalid state that can be restarted per minute")

	flag.BoolVar(&disableLeaderElection, "disable-leader-election", false, "Disable leader election for operator")
	flag.BoolVar(&disableServiceReconciler, "disable-service-reconciler", false, "Disable the Service reconciler for Dapr-enabled Deployments and StatefulSets")
	flag.StringVar(&watchNamespace, "watch-namespace", "", "Namespace to watch Dapr annotated resources in")
	flag.BoolVar(&enableArgoRolloutServiceReconciler, "enable-argo-rollout-service-reconciler", false, "Enable the service reconciler for Dapr-enabled Argo Rollouts")
	flag.BoolVar(&watchdogCanPatchPodLabels, "watchdog-can-patch-pod-labels", false, "Allow watchdog to patch pod labels to set pods with sidecar present")

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
