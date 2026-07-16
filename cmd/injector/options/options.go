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
	"path/filepath"

	"github.com/spf13/pflag"
	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/kit/logger"
)

type Options struct {
	HealthzPort          int
	HealthzListenAddress string
	Kubeconfig           string
	Port                 int
	ListenAddress        string
	SchedulerEnabled     bool
	Logger               logger.Options
	Metrics              *metrics.FlagOptions
}

func New(origArgs []string) *Options {
	var opts Options

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

	// Create a flag set
	fs := pflag.NewFlagSet("injector", pflag.ExitOnError)
	fs.SortFlags = true

	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port used for health checks")
	fs.StringVar(&opts.HealthzListenAddress, "healthz-listen-address", "", "The listening address for the healthz server")
	fs.IntVar(&opts.Port, "port", 4000, "The port used for the injector service")
	fs.StringVar(&opts.ListenAddress, "listen-address", "", "The listen address for the injector service")
	fs.BoolVar(&opts.SchedulerEnabled, "scheduler-enabled", true, "Marks if scheduler is enabled in the cluster, and address should be patched on sidecars.")

	if home := homedir.HomeDir(); home != "" {
		fs.StringVar(&opts.Kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		fs.StringVar(&opts.Kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	opts.Metrics = metrics.DefaultFlagOptions()
	opts.Metrics.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	// Ignore errors; flagset is set for ExitOnError
	_ = fs.Parse(args)

	return &opts
}
