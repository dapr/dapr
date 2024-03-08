/*
Copyright 2023 The Dapr Authors
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
	"fmt"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/logger"
)

type Options struct {
	Port        int
	HealthzPort int

	EtcdDataDir      string
	ListenAddress    string
	TLSEnabled       bool
	TrustDomain      string
	TrustAnchorsFile string
	SentryAddress    string
	PlacementAddress string
	Mode             string

	Logger  logger.Options
	Metrics *metrics.Options
}

func New() *Options {
	var opts Options

	flag.IntVar(&opts.Port, "port", 50006, "The port for the scheduler server to listen on")
	flag.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")

	flag.StringVar(&opts.ListenAddress, "listen-address", "", "The address for the Scheduler to listen on")
	flag.BoolVar(&opts.TLSEnabled, "tls-enabled", false, "Should TLS be enabled for the scheduler gRPC server")
	flag.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "Trust domain for the Dapr control plane")
	flag.StringVar(&opts.TrustAnchorsFile, "trust-anchors-file", securityConsts.ControlPlaneDefaultTrustAnchorsPath, "Filepath to the trust anchors for the Dapr control plane")
	flag.StringVar(&opts.SentryAddress, "sentry-address", fmt.Sprintf("dapr-sentry.%s.svc:443", security.CurrentNamespace()), "Address of the Sentry service")
	flag.StringVar(&opts.PlacementAddress, "placement-address", "", "Addresses for Dapr Actor Placement service")
	flag.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr Scheduler")
	flag.StringVar(&opts.EtcdDataDir, "etcd-data-dir", "./data", "Directory to store scheduler etcd data")

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	return &opts
}
