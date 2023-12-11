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
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/kit/logger"
)

const (
	//nolint:gosec
	defaultCredentialsPath = "/var/run/secrets/dapr.io/credentials"

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"
)

type Options struct {
	ConfigName            string
	Port                  int
	HealthzPort           int
	IssuerCredentialsPath string
	TrustDomain           string
	Kubeconfig            string
	Logger                logger.Options
	Metrics               *metrics.Options

	RootCAFilename     string
	IssuerCertFilename string
	IssuerKeyFilename  string
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

	// Create a flag set
	fs := pflag.NewFlagSet("sentry", pflag.ExitOnError)
	fs.SortFlags = true

	fs.StringVar(&opts.ConfigName, "config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")
	fs.StringVar(&opts.IssuerCredentialsPath, "issuer-credentials", defaultCredentialsPath, "Path to the credentials directory holding the issuer data")
	fs.StringVar(&opts.RootCAFilename, "issuer-ca-filename", config.DefaultRootCertFilename, "Certificate Authority certificate filename")
	fs.StringVar(&opts.IssuerCertFilename, "issuer-certificate-filename", config.DefaultIssuerCertFilename, "Issuer certificate filename")
	fs.StringVar(&opts.IssuerKeyFilename, "issuer-key-filename", config.DefaultIssuerKeyFilename, "Issuer private key filename")
	fs.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "The CA trust domain")
	fs.IntVar(&opts.Port, "port", config.DefaultPort, "The port for the sentry server to listen on")
	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")

	if home := homedir.HomeDir(); home != "" {
		fs.StringVar(&opts.Kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		fs.StringVar(&opts.Kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(fs.StringVar, fs.BoolVar)

	// Ignore errors; flagset is set for ExitOnError
	_ = fs.Parse(args)

	return &opts
}
