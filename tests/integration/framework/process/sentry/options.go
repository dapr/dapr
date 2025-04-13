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

package sentry

import (
	"github.com/dapr/dapr/pkg/sentry/server/ca"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
)

// options contains the options for running Sentry in integration tests.
type options struct {
	execOpts []exec.Option

	bundle        *ca.Bundle
	writeBundle   bool
	port          int
	healthzPort   int
	metricsPort   int
	configuration string
	writeConfig   bool
	kubeconfig    *string
	trustDomain   *string
	namespace     *string
	mode          *string

	// JWT options
	enableJWT         bool
	jwtSigningKeyFile *string
	jwksFile          *string

	// OIDC options
	oidcHTTPPort    *int
	oidcJWKSURI     *string
	oidcPathPrefix  *string
	oidcDomains     []string
	oidcTLSCertFile *string
	oidcTLSKeyFile  *string
}

// Option is a function that configures the process.
type Option func(*options)

func WithExecOptions(execOptions ...exec.Option) Option {
	return func(o *options) {
		o.execOpts = execOptions
	}
}

func WithPort(port int) Option {
	return func(o *options) {
		o.port = port
	}
}

func WithMetricsPort(port int) Option {
	return func(o *options) {
		o.metricsPort = port
	}
}

func WithHealthzPort(port int) Option {
	return func(o *options) {
		o.healthzPort = port
	}
}

func WithCABundle(bundle ca.Bundle) Option {
	return func(o *options) {
		o.bundle = &bundle
	}
}

func WithConfiguration(config string) Option {
	return func(o *options) {
		o.configuration = config
	}
}

func WithWriteTrustBundle(writeBundle bool) Option {
	return func(o *options) {
		o.writeBundle = writeBundle
	}
}

func WithKubeconfig(kubeconfig string) Option {
	return func(o *options) {
		o.kubeconfig = &kubeconfig
	}
}

func WithTrustDomain(trustDomain string) Option {
	return func(o *options) {
		o.trustDomain = &trustDomain
	}
}

func WithWriteConfig(write bool) Option {
	return func(o *options) {
		o.writeConfig = write
	}
}

func WithNamespace(namespace string) Option {
	return func(o *options) {
		o.namespace = &namespace
	}
}

func WithMode(mode string) Option {
	return func(o *options) {
		o.mode = &mode
	}
}

// WithEnableJWT enables JWT token issuance in Sentry
func WithEnableJWT(enable bool) Option {
	return func(o *options) {
		o.enableJWT = enable
	}
}

// WithJWTSigningKeyFile specifies a custom JWT signing key file
func WithJWTSigningKeyFile(keyFile string) Option {
	return func(o *options) {
		o.jwtSigningKeyFile = &keyFile
	}
}

// WithJWKSFile specifies a custom JWKS file
func WithJWKSFile(jwksFile string) Option {
	return func(o *options) {
		o.jwksFile = &jwksFile
	}
}

// WithOIDCHTTPPort sets the port for the OIDC HTTP server
func WithOIDCHTTPPort(port int) Option {
	return func(o *options) {
		o.oidcHTTPPort = &port
	}
}

// WithOIDCJWKSURI sets the custom URI where the JWKS can be accessed externally
func WithOIDCJWKSURI(jwksURI string) Option {
	return func(o *options) {
		o.oidcJWKSURI = &jwksURI
	}
}

// WithOIDCPathPrefix sets the path prefix to add to all OIDC HTTP endpoints
func WithOIDCPathPrefix(prefix string) Option {
	return func(o *options) {
		o.oidcPathPrefix = &prefix
	}
}

// WithOIDCDomains sets the list of allowed domains for OIDC HTTP endpoint requests
func WithOIDCDomains(hosts []string) Option {
	return func(o *options) {
		o.oidcDomains = hosts
	}
}

// WithOIDCTLSCertFile sets the TLS certificate file for the OIDC HTTP server
func WithOIDCTLSCertFile(certFile string) Option {
	return func(o *options) {
		o.oidcTLSCertFile = &certFile
	}
}

// WithOIDCTLSKeyFile sets the TLS key file for the OIDC HTTP server
func WithOIDCTLSKeyFile(keyFile string) Option {
	return func(o *options) {
		o.oidcTLSKeyFile = &keyFile
	}
}
