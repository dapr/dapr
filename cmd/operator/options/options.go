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

	"github.com/spf13/pflag"

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
	AdditionalCPServices               []string
}

func New(origArgs []string) *Options {
	// We are using pflag to parse the CLI flags
	// pflag is a drop-in replacement for the standard library's "flag" package, however…
	// There's one key difference: with the stdlib's "flag" package, there are no short-hand options so options can be defined with a single slash (such as "daprd -mode").
	// With pflag, single slashes are reserved for shorthands.
	// So, we are doing this thing where we iterate through all args and double-up the slash if it's single
	// This works *as long as* we don't start using shorthand flags (which haven't been in use so far).
	args := make([]string, len(origArgs))
	for i, a := range origArgs {
		if len(a) > 2 && a[0] == '-' && a[1] != '-' {
			args[i] = "-" + a
		} else {
			args[i] = a
		}
	}
	var opts Options

	// This resets the flags on klog, which will otherwise try to log to the FS.
	klogFlags := flag.NewFlagSet("klog", flag.ExitOnError)
	klog.InitFlags(klogFlags)
	klogFlags.Set("logtostderr", "true")

	// Create a flag set
	fs := pflag.NewFlagSet("operator", pflag.ExitOnError)
	fs.SortFlags = true

	fs.StringVar(&opts.Config, "config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")

	fs.StringVar(&opts.watchdogIntervalStr, "watch-interval", defaultWatchInterval, "Interval for polling pods' state, e.g. '2m'. Set to '0' to disable, or 'once' to only run once when the operator starts")
	fs.IntVar(&opts.MaxPodRestartsPerMinute, "max-pod-restarts-per-minute", defaultMaxPodRestartsPerMinute, "Maximum number of pods in an invalid state that can be restarted per minute")

	fs.BoolVar(&opts.DisableLeaderElection, "disable-leader-election", false, "Disable leader election for operator")
	fs.BoolVar(&opts.DisableServiceReconciler, "disable-service-reconciler", false, "Disable the Service reconciler for Dapr-enabled Deployments and StatefulSets")
	fs.StringVar(&opts.WatchNamespace, "watch-namespace", "", "Namespace to watch Dapr annotated resources in")
	fs.BoolVar(&opts.EnableArgoRolloutServiceReconciler, "enable-argo-rollout-service-reconciler", false, "Enable the service reconciler for Dapr-enabled Argo Rollouts")
	fs.BoolVar(&opts.WatchdogCanPatchPodLabels, "watchdog-can-patch-pod-labels", false, "Allow watchdog to patch pod labels to set pods with sidecar present")

	fs.StringVar(&opts.TrustAnchorsFile, "trust-anchors-file", securityConsts.ControlPlaneDefaultTrustAnchorsPath, "Filepath to the trust anchors for the Dapr control plane")

	fs.IntVar(&opts.APIPort, "port", 6500, "The port for the operator API server to listen on")
	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")

	fs.StringSliceVar(&opts.AdditionalCPServices, "additional-control-plane-services", []string{"placement"}, "Name of the additional control plane services, if any")

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	// Ignore errors; flagset is set for ExitOnError
	_ = fs.Parse(args)

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
