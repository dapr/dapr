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
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/client-go/util/homedir"

	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
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

	X509 X509Options
	JWT  JWTOptions
	OIDC OIDCOptions
}

type X509Options struct {
	RootCAFilename     string
	IssuerCertFilename string
	IssuerKeyFilename  string
}

type JWTOptions struct {
	Enabled            bool
	SigningKeyFilename string
	JWKSFilename       string
	Issuer             *string
	SigningAlgorithm   string
	KeyID              *string
	TTL                time.Duration

	// sentinel values used to bind to potentially nil pointers
	issuer string // Issuer value for JWT tokens (no issuer if empty)
	keyID  string // Key ID (kid) used for JWT signing (defaults to base64 encoded SHA-256 of the signing key)
}

type OIDCOptions struct {
	Enabled             bool
	ServerListenAddress string
	ServerListenPort    int
	TLSEnabled          bool     // Enable TLS for the OIDC HTTP server
	TLSCertFile         *string  // TLS certificate file for the OIDC HTTP server (required when OIDC HTTP server is enabled)
	TLSKeyFile          *string  // TLS key file for the OIDC HTTP server (required when OIDC HTTP server is enabled)
	AllowedHosts        []string // Domains that public endpoints can be accessed from (Optional)
	JWKSURI             *string  // Force the public JWKS URI to this value (Optional)
	PathPrefix          *string  // Path prefix for HTTP endpoints (Optional)
	Insecure            bool     // Allow HTTP insecure (Optional)

	// sentinel values used to bind to potentially nil pointers
	jwksURI     string // Custom URI where the JWKS can be accessed externally
	pathPrefix  string // Path prefix to add to OIDC HTTP endpoints
	tlsCertFile string // TLS certificate file for the OIDC HTTP server
	tlsKeyFile  string // TLS key file for the OIDC HTTP server
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
	fs.StringVar(&opts.X509.RootCAFilename, "issuer-ca-filename", config.DefaultRootCertFilename, "Certificate Authority certificate filename")
	fs.StringVar(&opts.X509.IssuerCertFilename, "issuer-certificate-filename", config.DefaultIssuerCertFilename, "Issuer certificate filename")
	fs.StringVar(&opts.X509.IssuerKeyFilename, "issuer-key-filename", config.DefaultIssuerKeyFilename, "Issuer private key filename")
	fs.StringVar(&opts.TrustDomain, "trust-domain", "localhost", "The CA trust domain")
	fs.IntVar(&opts.Port, "port", config.DefaultPort, "The port for the sentry server to listen on")
	fs.StringVar(&opts.ListenAddress, "listen-address", "", "The listen address for the sentry server")
	fs.IntVar(&opts.HealthzPort, "healthz-port", 8080, "The port for the healthz server to listen on")
	fs.StringVar(&opts.HealthzListenAddress, "healthz-listen-address", "", "The listening address for the healthz server")
	fs.StringVar(&opts.Mode, "mode", string(modes.StandaloneMode), "Runtime mode for Dapr Sentry")
	fs.BoolVar(&opts.JWT.Enabled, "jwt-enabled", false, "Enable JWT token issuance by Sentry")
	fs.StringVar(&opts.JWT.SigningKeyFilename, "jwt-key-filename", config.DefaultJWTSigningKeyFilename, "JWT signing key filename")
	fs.StringVar(&opts.JWT.JWKSFilename, "jwks-filename", config.DefaultJWKSFilename, "JWKS (JSON Web Key Set) filename")
	fs.StringVar(&opts.JWT.issuer, "jwt-issuer", "", "Issuer value for JWT tokens (no issuer if empty)")
	fs.StringVar(&opts.JWT.SigningAlgorithm, "jwt-signing-algorithm", string(bundle.DefaultJWTSignatureAlgorithm), "Algorithm used for JWT signing, must be supported by signing key")
	fs.StringVar(&opts.JWT.keyID, "jwt-key-id", "", "Key ID (kid) used for JWT signing (defaults to base64 encoded SHA-256 of the signing key)")
	fs.DurationVar(&opts.JWT.TTL, "jwt-ttl", config.DefaultJWTTTL, "Time-to-live for JWT tokens (default 24h)")
	fs.BoolVar(&opts.OIDC.Enabled, "oidc-enabled", false, "Enable OIDC HTTP server for Dapr Sentry")
	fs.IntVar(&opts.OIDC.ServerListenPort, "oidc-server-listen-port", 9080, "The port for the OIDC HTTP server")
	fs.StringVar(&opts.OIDC.ServerListenAddress, "oidc-server-listen-address", "localhost", "The address for the OIDC HTTP server")
	fs.BoolVar(&opts.OIDC.TLSEnabled, "oidc-server-tls-enabled", true, "Serve OIDC HTTP with TLS")
	fs.StringVar(&opts.OIDC.tlsCertFile, "oidc-server-tls-cert-file", "", "TLS certificate file for the OIDC HTTP server (required when OIDC HTTP server is enabled)")
	fs.StringVar(&opts.OIDC.tlsKeyFile, "oidc-server-tls-key-file", "", "TLS key file for the OIDC HTTP server (required when OIDC HTTP server is enabled)")
	fs.StringVar(&opts.OIDC.jwksURI, "oidc-jwks-uri", "", "Custom URI where the JWKS can be accessed externally")
	fs.StringVar(&opts.OIDC.pathPrefix, "oidc-path-prefix", "", "Path prefix to add to OIDC HTTP endpoints")
	fs.StringSliceVar(&opts.OIDC.AllowedHosts, "oidc-allowed-hosts", nil, "List of allowed hosts for OIDC HTTP endpoints")

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

	if fs.Changed("oidc-server-tls-cert-file") {
		opts.OIDC.TLSCertFile = &opts.OIDC.tlsCertFile
	}
	if fs.Changed("oidc-server-tls-key-file") {
		opts.OIDC.TLSKeyFile = &opts.OIDC.tlsKeyFile
	}
	if fs.Changed("oidc-jwks-uri") {
		opts.OIDC.JWKSURI = &opts.OIDC.jwksURI
	}
	if fs.Changed("oidc-path-prefix") {
		opts.OIDC.PathPrefix = &opts.OIDC.pathPrefix
	}
	if fs.Changed("jwt-key-id") {
		opts.JWT.KeyID = &opts.JWT.keyID
	}
	if fs.Changed("jwt-issuer") {
		opts.JWT.Issuer = &opts.JWT.issuer
	}

	return &opts
}

func (o *Options) Validate() error {
	if o.Mode != string(modes.KubernetesMode) && o.Mode != string(modes.StandaloneMode) {
		return fmt.Errorf("invalid mode: %s", o.Mode)
	}

	// Validate OIDC TLS configuration when OIDC HTTP server is enabled and not in insecure mode
	if o.OIDC.Enabled && o.OIDC.TLSEnabled {
		if o.OIDC.TLSCertFile == nil {
			return errors.New("oidc-server-tls-cert-file is required when OIDC HTTP server is enabled (unless oidc-server-tls-enabled is false)")
		}
		if o.OIDC.TLSKeyFile == nil {
			return errors.New("oidc-server-tls-key-file is required when OIDC HTTP server is enabled (unless oidc-server-tls-enabled is false)")
		}
	}

	if o.JWT.Issuer != nil && !o.JWT.Enabled {
		return errors.New("jwt-issuer cannot be set when jwt-enabled is false")
	}

	return nil
}
