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

	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/kit/logger"
)

type Options struct {
	HealthzPort int
	Kubeconfig  string
	Logger      logger.Options
	Metrics     *metrics.Options
}

func New() *Options {
	var opts Options

	flag.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port used for health checks")

	flag.StringVar(&credentials.RootCertFilename, "issuer-ca-secret-key", credentials.RootCertFilename, "Certificate Authority certificate secret key")
	flag.StringVar(&credentials.IssuerCertFilename, "issuer-certificate-secret-key", credentials.IssuerCertFilename, "Issuer certificate secret key")
	flag.StringVar(&credentials.IssuerKeyFilename, "issuer-key-secret-key", credentials.IssuerKeyFilename, "Issuer private key secret key")

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
