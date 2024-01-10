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

const (
	//nolint:gosec
	defaultCredentialsPath = "/var/run/secrets/dapr.io/credentials"

	// defaultDaprSystemConfigName is the default resource object name for Dapr System Config.
	defaultDaprSystemConfigName = "daprsystem"
	defaultHealthzPort          = 7070
	defaultSchedulerPort        = 50006
)

type Options struct {
	SchedulerPort int
	HealthzPort   int

	TLSEnabled       bool
	TrustDomain      string
	TrustAnchorsFile string
	SentryAddress    string
	Mode             string

	Logger  logger.Options
	Metrics *metrics.Options
}

func New() *Options {
	var opts Options

	flag.IntVar(&opts.SchedulerPort, "port", defaultSchedulerPort, "The port for the scheduler server to listen on")
	flag.IntVar(&opts.HealthzPort, "healthz-port", defaultHealthzPort, "The port for the healthz server to listen on")

	flag.BoolVar(&opts.TLSEnabled, "tls-enabled", false, "Should TLS be enabled for the scheduler gRPC server")
	flag.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "Trust domain for the Dapr control plane")
	flag.StringVar(&opts.TrustAnchorsFile, "trust-anchors-file", securityConsts.ControlPlaneDefaultTrustAnchorsPath, "Filepath to the trust anchors for the Dapr control plane")
	flag.StringVar(&opts.SentryAddress, "sentry-address", fmt.Sprintf("dapr-sentry.%s.svc:443", security.CurrentNamespace()), "Address of the Sentry service")
	flag.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Placement")

	opts.Logger = logger.DefaultOptions()
	opts.Logger.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	opts.Metrics = metrics.DefaultMetricOptions()
	opts.Metrics.AttachCmdFlags(flag.StringVar, flag.BoolVar)

	flag.Parse()

	return &opts
}
