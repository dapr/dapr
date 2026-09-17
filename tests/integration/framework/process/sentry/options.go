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
	"testing"
	"time"

	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry/server/ca/bundle"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	prockube "github.com/dapr/dapr/tests/integration/framework/process/kubernetes"
)

// options contains the options for running Sentry in integration tests.
type options struct {
	execOpts []exec.Option

	bundle         *bundle.Bundle
	writeBundle    bool
	credentialsDir *string
	port           int
	healthzPort    int
	metricsPort    int
	configuration  string
	writeConfig    bool
	kubeconfig     *string
	trustDomain    *string
	namespace      *string
	mode           *string

	jwt      jwtOptions
	oidc     oidcOptions
	rotation rotationOptions
}

type rotationOptions struct {
	enabled           *bool
	triggerWindow     *time.Duration
	propagationWindow *time.Duration
	checkInterval     *time.Duration
}

type jwtOptions struct {
	enabled           bool
	issuer            *string
	ttl               *time.Duration
	jwtIssuerFromOIDC bool
	keyID             *string // optional explicit kid
}

type oidcOptions struct {
	enabled          bool
	serverListenPort *int
	jwksURI          *string
	pathPrefix       *string
	allowedHosts     []string
	tlsCertFile      *string
	tlsKeyFile       *string
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

func WithCABundle(bundle bundle.Bundle) Option {
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

// WithCredentialsDirectory uses the given directory for issuer credentials
// instead of a fresh temporary directory. Combine with
// WithWriteTrustBundle(false) to start a sentry from another sentry's
// on-disk bundle, e.g. to exercise restarts.
func WithCredentialsDirectory(dir string) Option {
	return func(o *options) {
		o.credentialsDir = &dir
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

// WithKubeAPI configures sentry for Kubernetes mode against the given mock
// Kubernetes API server, running in the given namespace.
func WithKubeAPI(t *testing.T, kubeAPI *prockube.Kubernetes, namespace string) Option {
	return func(o *options) {
		WithWriteConfig(false)(o)
		WithKubeconfig(kubeAPI.KubeconfigPath(t))(o)
		WithNamespace(namespace)(o)
		WithMode(string(modes.KubernetesMode))(o)
		WithExecOptions(exec.WithEnvVars(t, "KUBERNETES_SERVICE_HOST", "anything"))(o)
	}
}

// WithEnableJWT enables JWT token issuance in Sentry
func WithEnableJWT(enable bool) Option {
	return func(o *options) {
		o.jwt.enabled = enable
	}
}

// WithJWTIssuer sets the JWT issuer for Sentry
func WithJWTIssuer(issuer string) Option {
	return func(o *options) {
		o.jwt.issuer = &issuer
	}
}

// WithJWTTTL sets the JWT time-to-live (TTL) for Sentry
func WithJWTTTL(ttl time.Duration) Option {
	return func(o *options) {
		o.jwt.ttl = &ttl
	}
}

// WithJWTIssuerFromOIDC uses the OIDC issuer as the JWT issuer in Sentry.
func WithJWTIssuerFromOIDC() Option {
	return func(o *options) {
		o.jwt.jwtIssuerFromOIDC = true
	}
}

// WithOIDCEnabled enables the OIDC HTTP server in Sentry
func WithOIDCEnabled(enabled bool) Option {
	return func(o *options) {
		o.oidc.enabled = enabled
	}
}

// WithOIDCServerListenPort sets the port for the OIDC HTTP server
func WithOIDCServerListenPort(port int) Option {
	return func(o *options) {
		o.oidc.serverListenPort = &port
	}
}

// WithOIDCJWKSURI sets the custom URI where the JWKS can be accessed externally
func WithOIDCJWKSURI(jwksURI string) Option {
	return func(o *options) {
		o.oidc.jwksURI = &jwksURI
	}
}

// WithOIDCPathPrefix sets the path prefix to add to all OIDC HTTP endpoints
func WithOIDCPathPrefix(prefix string) Option {
	return func(o *options) {
		o.oidc.pathPrefix = &prefix
	}
}

// WithOIDCAllowedHosts sets the list of allowed hosts for OIDC HTTP endpoint requests
func WithOIDCAllowedHosts(hosts []string) Option {
	return func(o *options) {
		o.oidc.allowedHosts = hosts
	}
}

// WithOIDCTLSCertFile sets the TLS certificate file for the OIDC HTTP server
func WithOIDCTLSCertFile(certFile string) Option {
	return func(o *options) {
		o.oidc.tlsCertFile = &certFile
	}
}

// WithOIDCTLSKeyFile sets the TLS key file for the OIDC HTTP server
func WithOIDCTLSKeyFile(keyFile string) Option {
	return func(o *options) {
		o.oidc.tlsKeyFile = &keyFile
	}
}

// WithJWTKeyID sets an explicit JWT key ID to pass via --jwt-key-id flag
func WithJWTKeyID(kid string) Option {
	return func(o *options) {
		o.jwt.keyID = &kid
	}
}

// WithRotationEnabled enables automatic root CA rotation, which is off by
// default.
func WithRotationEnabled(enabled bool) Option {
	return func(o *options) {
		o.rotation.enabled = &enabled
	}
}

// WithRotationTriggerWindow sets how long before root CA expiry automatic
// rotation begins.
func WithRotationTriggerWindow(window time.Duration) Option {
	return func(o *options) {
		o.rotation.triggerWindow = &window
	}
}

// WithRotationPropagationWindow sets how long combined trust anchors are
// distributed before signing switches to the new issuer.
func WithRotationPropagationWindow(window time.Duration) Option {
	return func(o *options) {
		o.rotation.propagationWindow = &window
	}
}

// WithRotationCheckInterval sets how often the rotation loop polls root CA
// expiry.
func WithRotationCheckInterval(interval time.Duration) Option {
	return func(o *options) {
		o.rotation.checkInterval = &interval
	}
}
