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

package app

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"time"

	"github.com/dapr/dapr/cmd/sentry/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	"github.com/dapr/dapr/pkg/healthz"
	healthzserver "github.com/dapr/dapr/pkg/healthz/server"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/sentry"
	"github.com/dapr/dapr/pkg/sentry/config"
	"github.com/dapr/dapr/pkg/sentry/monitoring"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/signals"
)

var log = logger.NewLogger("dapr.sentry")

func Run() {
	opts := options.New(os.Args[1:])

	if err := opts.Validate(); err != nil {
		log.Fatalf("Invalid options: %s", err)
	}

	// Apply options to all loggers
	if err := logger.ApplyOptionsToLoggers(&opts.Logger); err != nil {
		log.Fatal(err)
	}

	log.Infof("Starting Dapr Sentry certificate authority -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("Log level set to: %s", opts.Logger.OutputLevel)

	if err := utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: opts.Kubeconfig,
	}); err != nil {
		log.Fatalf("Error setting env: %v", err)
	}

	if err := monitoring.InitMetrics(); err != nil {
		log.Fatal(err)
	}

	var (
		issuerEvent = make(chan struct{})
		mngr        = concurrency.NewRunnerManager()
	)

	issuerCertPath := filepath.Join(opts.IssuerCredentialsPath, opts.IssuerCertFilename)
	if filepath.IsAbs(opts.IssuerCertFilename) {
		log.Debugf("Using user provided issuer cert path: %s", opts.IssuerCertFilename)
		issuerCertPath = opts.IssuerCertFilename
	}
	issuerKeyPath := filepath.Join(opts.IssuerCredentialsPath, opts.IssuerKeyFilename)
	if filepath.IsAbs(opts.IssuerKeyFilename) {
		log.Debugf("Using user provided issuer key path: %s", opts.IssuerKeyFilename)
		issuerKeyPath = opts.IssuerKeyFilename
	}
	rootCertPath := filepath.Join(opts.IssuerCredentialsPath, opts.RootCAFilename)
	if filepath.IsAbs(opts.RootCAFilename) {
		log.Debugf("Using user provided root cert path: %s", opts.RootCAFilename)
		rootCertPath = opts.RootCAFilename
	}
	jwtKeyPath := filepath.Join(opts.IssuerCredentialsPath, config.DefaultJWTSigningKeyFilename)
	if filepath.IsAbs(opts.JWTSigningKeyFilename) {
		log.Debugf("Using user provided JWT signing key path: %s", opts.JWTSigningKeyFilename)
		jwtKeyPath = opts.JWTSigningKeyFilename
	}
	jwksPath := filepath.Join(opts.IssuerCredentialsPath, config.DefaultJWKSFilename)
	if filepath.IsAbs(opts.JWKSFilename) {
		log.Debugf("Using user provided JWKS path: %s", opts.JWKSFilename)
		jwksPath = opts.JWKSFilename
	}

	m := make(map[string]struct{})
	// we need to watch over all these relevant directories
	for _, path := range []string{
		issuerCertPath,
		issuerKeyPath,
		rootCertPath,
		jwtKeyPath,
		jwksPath,
	} {
		if path != "" {
			dir := filepath.Dir(path)
			if _, ok := m[dir]; !ok {
				m[dir] = struct{}{}
			}
		}
	}

	watchDirs := make([]string, 0, len(m))
	for dir := range m {
		watchDirs = append(watchDirs, dir)
	}

	cfg, err := config.FromConfigName(opts.ConfigName, opts.Mode)
	if err != nil {
		log.Fatal(err)
	}

	cfg.IssuerCertPath = issuerCertPath
	cfg.IssuerKeyPath = issuerKeyPath
	cfg.RootCertPath = rootCertPath
	cfg.JWTSigningKeyPath = jwtKeyPath
	cfg.JWTEnabled = opts.JWTEnabled
	cfg.JWKSPath = jwksPath
	cfg.TrustDomain = opts.TrustDomain
	cfg.Port = opts.Port
	cfg.ListenAddress = opts.ListenAddress
	cfg.Mode = modes.DaprMode(opts.Mode)

	if opts.JWTIssuer != "" {
		cfg.JWTIssuer = &opts.JWTIssuer
	}

	if len(opts.JWTAudiences) > 0 {
		cfg.JWTAudiences = opts.JWTAudiences
	}

	if opts.JWTSigningAlgorithm != "" {
		cfg.JWTSigningAlgorithm = opts.JWTSigningAlgorithm
	}

	if opts.JWTKeyID != "" {
		cfg.JWTKeyID = opts.JWTKeyID
	}

	// We use runner manager inception here since we want the inner manager to be
	// restarted when the CA server needs to be restarted because of file events.
	// We don't want to restart the healthz server and file watcher on file
	// events (as well as wanting to terminate the program on signals).
	caMngrFactory := func(ctx context.Context) error {
		healthz := healthz.New()
		metricsExporter := metrics.New(metrics.Options{
			Log:       log,
			Enabled:   opts.Metrics.Enabled(),
			Namespace: metrics.DefaultMetricNamespace,
			Port:      opts.Metrics.Port(),
			Healthz:   healthz,
		})

		// Configure TLS for OIDC HTTP server if needed
		var oidcTLSConfig *tls.Config
		if opts.OIDCTLSCertFile != "" && opts.OIDCTLSKeyFile != "" {
			var err error
			oidcTLSConfig, err = createOIDCTLSConfig(opts.OIDCTLSCertFile, opts.OIDCTLSKeyFile)
			if err != nil {
				log.Errorf("Failed to create OIDC TLS config: %v", err)
				return err
			}
		}

		sentry, serr := sentry.New(ctx, sentry.Options{
			Config:         cfg,
			Healthz:        healthz,
			OIDCHTTPPort:   opts.OIDCHTTPPort,
			OIDCDomains:    opts.OIDCDomains,
			OIDCJWKSURI:    opts.OIDCJWKSURI,
			ODICPathPrefix: opts.OIDCPathPrefix,
			OIDCTLSConfig:  oidcTLSConfig,
			OIDCInsecure:   opts.OIDCTLSInsecure,
		})
		if serr != nil {
			return serr
		}
		return concurrency.NewRunnerManager(
			healthzserver.New(healthzserver.Options{
				Log:     log,
				Port:    opts.HealthzPort,
				Healthz: healthz,
			}).Start,
			metricsExporter.Start,
			sentry.Start,
			func(ctx context.Context) error {
				select {
				case <-ctx.Done():
					return nil

				case <-issuerEvent:
					monitoring.IssuerCertChanged()
					log.Debug("Received issuer credentials changed signal")

					select {
					case <-ctx.Done():
						return nil
					// Batch all signals within 2s of each other
					case <-time.After(2 * time.Second):
						log.Warn("Issuer credentials changed; reloading")
						return nil
					}
				}
			},
		).Run(ctx)
	}

	// CA Server
	err = mngr.Add(func(ctx context.Context) error {
		for {
			if runErr := caMngrFactory(ctx); runErr != nil {
				return runErr
			}
			// Catch outer context cancellation to exit.
			select {
			case <-ctx.Done():
				return nil
			default:
			}
		}
	})
	if err != nil {
		log.Fatal(err)
	}

	// Watch for changes in the watchDirs
	fs, err := fswatcher.New(fswatcher.Options{
		Targets: watchDirs,
	})
	if err != nil {
		log.Fatal(err)
	}
	if err = mngr.Add(func(ctx context.Context) error {
		log.Infof("Starting watch on filesystem directories: %v", watchDirs)
		return fs.Run(ctx, issuerEvent)
	}); err != nil {
		log.Fatal(err)
	}

	// Run the runner manager.
	if err := mngr.Run(signals.Context()); err != nil {
		log.Fatal(err)
	}
	log.Info("Sentry shut down gracefully")
}

// createOIDCTLSConfig creates a TLS configuration for the OIDC HTTP server
func createOIDCTLSConfig(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}, nil
}
