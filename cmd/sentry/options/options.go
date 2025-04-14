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
	"fmt"
	"path/filepath"

	"github.com/spf13/pflag"
	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
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
	ListenAddress         string
	HealthzPort           int
	HealthzListenAddress  string
	IssuerCredentialsPath string
	TrustDomain           string
	Kubeconfig            string
	Logger                logger.Options
	Metrics               *metrics.FlagOptions
	Mode                  string

	RootCAFilename        string
	IssuerCertFilename    string
	IssuerKeyFilename     string
	JWTSigningKeyFilename string
	JWKSFilename          string
	JWTEnabled            bool
	JWTIssuer             string
	OIDCHTTPPort          int
	OIDCJWKSURI           string
	OIDCPathPrefix        string
	OIDCDomains           []string
	OIDCTLSCertFile       string
	OIDCTLSKeyFile        string
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
	fs.StringVar(&opts.ListenAddress, "listen-address", "", "The listen address for the sentry server")
	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")
	fs.StringVar(&opts.HealthzListenAddress, "healthz-listen-address", "", "The listening address for the healthz server")
	fs.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr Sentry")
	fs.BoolVar(&opts.JWTEnabled, "jwt-enabled", false, "Enable JWT token issuance by Sentry")
	fs.StringVar(&opts.JWTSigningKeyFilename, "jwt-key-filename", config.DefaultJWTSigningKeyFilename, "JWT signing key filename")
	fs.StringVar(&opts.JWKSFilename, "jwks-filename", config.DefaultJWKSFilename, "JWKS (JSON Web Key Set) filename")
	fs.StringVar(&opts.JWTIssuer, "jwt-issuer", "", "Issuer value for JWT tokens (no issuer if empty)")
	fs.IntVar(&opts.OIDCHTTPPort, "oidc-http-port", 0, "The port for the OIDC HTTP server (disabled if 0)")
	fs.StringVar(&opts.OIDCJWKSURI, "oidc-jwks-uri", "", "Custom URI where the JWKS can be accessed externally")
	fs.StringVar(&opts.OIDCPathPrefix, "oidc-path-prefix", "", "Path prefix to add to all OIDC HTTP endpoints")
	fs.StringSliceVar(&opts.OIDCDomains, "oidc-domains", nil, "List of allowed domains for OIDC HTTP endpoint requests")
	fs.StringVar(&opts.OIDCTLSCertFile, "oidc-tls-cert-file", "", "TLS certificate file for the OIDC HTTP server (required when OIDC HTTP server is enabled)")
	fs.StringVar(&opts.OIDCTLSKeyFile, "oidc-tls-key-file", "", "TLS key file for the OIDC HTTP server (required when OIDC HTTP server is enabled)")

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

func (o *Options) Validate() error {
	if o.Mode != string(modes.KubernetesMode) && o.Mode != string(modes.StandaloneMode) {
		return fmt.Errorf("invalid mode: %s", o.Mode)
	}

	// Validate OIDC TLS configuration when OIDC HTTP server is enabled
	if o.OIDCHTTPPort > 0 {
		if o.OIDCTLSCertFile == "" {
			return fmt.Errorf("oidc-tls-cert-file is required when OIDC HTTP server is enabled")
		}
		if o.OIDCTLSKeyFile == "" {
			return fmt.Errorf("oidc-tls-key-file is required when OIDC HTTP server is enabled")
		}
	}

	if o.JWTIssuer != "" && !o.JWTEnabled {
		return fmt.Errorf("jwt-issuer cannot be set when jwt-enabled is false")
	}

	if o.JWTSigningKeyFilename != "" && !o.JWTEnabled {
		return fmt.Errorf("jwt-key-filename cannot be set when jwt-enabled is false")
	}

	return nil
}
