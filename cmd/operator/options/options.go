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

package options

import (
	"flag"
	"strings"
	"time"

	"k8s.io/klog"

	"github.com/dapr/dapr/pkg/metrics"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

const (
	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"

	// defaultWatchInterval is the default value for watch-interval, in seconds (note this is a string as `once` is an acceptable value too).
	defaultWatchInterval = "0"

	// defaultMaxPodRestartsPerMinute is the default value for max-pod-restarts-per-minute.
	defaultMaxPodRestartsPerMinute = 20
)

var log = logger.NewLogger("dapr.operator.options")

type Options struct {
	Config                             string
	MaxPodRestartsPerMinute            int
	DisableLeaderElection              bool
	DisableServiceReconciler           bool
	WatchNamespace                     string
	EnableArgoRolloutServiceReconciler bool
	WatchdogEnabled                    bool
	WatchdogInterval                   time.Duration
	watchdogIntervalStr                string
	WatchdogCanPatchPodLabels          bool
	TrustAnchorsFile                   string
	Logger                             logger.Options
	Metrics                            *metrics.Options
	APIPort                            int
	HealthzPort                        int
}

func New() *Options {
	var opts Options

	// This resets the flags on klog, which will otherwise try to log to the FS.
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	klogFlags.Set("logtostderr", "true")

	flag.StringVar(&opts.Config, "config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")

	flag.StringVar(&opts.watchdogIntervalStr, "watch-interval", defaultWatchInterval, "Interval for polling pods' state, e.g. '2m'. Set to '0' to disable, or 'once' to only run once when the operator starts")
	flag.IntVar(&opts.MaxPodRestartsPerMinute, "max-pod-restarts-per-minute", defaultMaxPodRestartsPerMinute, "Maximum number of pods in an invalid state that can be restarted per minute")

	flag.BoolVar(&opts.DisableLeaderElection, "disable-leader-election", false, "Disable leader election for operator")
	flag.BoolVar(&opts.DisableServiceReconciler, "disable-service-reconciler", false, "Disable the Service reconciler for Dapr-enabled Deployments and StatefulSets")
	flag.StringVar(&opts.WatchNamespace, "watch-namespace", "", "Namespace to watch Dapr annotated resources in")
	flag.BoolVar(&opts.EnableArgoRolloutServiceReconciler, "enable-argo-rollout-service-reconciler", false, "Enable the service reconciler for Dapr-enabled Argo Rollouts")
	flag.BoolVar(&opts.WatchdogCanPatchPodLabels, "watchdog-can-patch-pod-labels", false, "Allow watchdog to patch pod labels to set pods with sidecar present")

	flag.StringVar(&opts.TrustAnchorsFile, "trust-anchors-file", securityConsts.ControlPlaneDefaultTrustAnchorsPath, "Filepath to the trust anchors for the Dapr control plane")

	flag.IntVar(&opts.APIPort, "port", 6500, "The port for the operator API server to listen on")
	flag.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	wilc := strings.ToLower(opts.watchdogIntervalStr)
	switch wilc {
	case "0", "false", "f", "no", "off":
		// Disabled - do nothing
	default:
		opts.WatchdogEnabled = true
		if wilc != "once" {
			dur, err := time.ParseDuration(opts.watchdogIntervalStr)
			if err != nil {
				log.Fatalf("invalid value for watch-interval: %v", err)
			}
			if dur < time.Second {
				log.Fatalf("invalid watch-interval value: if not '0' or 'once', must be at least 1s")
			}
			opts.WatchdogInterval = dur
		}
	}

	return &opts
}
