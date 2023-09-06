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
	"path/filepath"

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
	TokenAudience         string
	Kubeconfig            string
	Logger                logger.Options
	Metrics               *metrics.Options

	RootCAFilename     string
	IssuerCertFilename string
	IssuerKeyFilename  string
}

func New() *Options {
	var opts Options

	flag.StringVar(&opts.ConfigName, "config", defaultDaprSystemConfigName, "Path to config file, or name of a configuration object")
	flag.StringVar(&opts.IssuerCredentialsPath, "issuer-credentials", defaultCredentialsPath, "Path to the credentials directory holding the issuer data")
	flag.StringVar(&opts.RootCAFilename, "issuer-ca-filename", config.DefaultRootCertFilename, "Certificate Authority certificate filename")
	flag.StringVar(&opts.IssuerCertFilename, "issuer-certificate-filename", config.DefaultIssuerCertFilename, "Issuer certificate filename")
	flag.StringVar(&opts.IssuerKeyFilename, "issuer-key-filename", config.DefaultIssuerKeyFilename, "Issuer private key filename")
	flag.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "The CA trust domain")
	flag.StringVar(&opts.TokenAudience, "token-audience", "", "DEPRECATED, flag has no effect.")
	flag.IntVar(&opts.Port, "port", config.DefaultPort, "The port for the sentry server to listen on")
	flag.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")

	if home := homedir.HomeDir(); home != "" {
		flag.StringVar(&opts.Kubeconfig, "kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		flag.StringVar(&opts.Kubeconfig, "kubeconfig", "", "absolute path to the kubeconfig file")
	}

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	return &opts
}
