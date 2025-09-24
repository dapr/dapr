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

	issuerCertPath := filepath.Join(opts.IssuerCredentialsPath, opts.X509.IssuerCertFilename)
	if filepath.IsAbs(opts.X509.IssuerCertFilename) {
		log.Debugf("Using user provided issuer cert path: %s", opts.X509.IssuerCertFilename)
		issuerCertPath = opts.X509.IssuerCertFilename
	}
	issuerKeyPath := filepath.Join(opts.IssuerCredentialsPath, opts.X509.IssuerKeyFilename)
	if filepath.IsAbs(opts.X509.IssuerKeyFilename) {
		log.Debugf("Using user provided issuer key path: %s", opts.X509.IssuerKeyFilename)
		issuerKeyPath = opts.X509.IssuerKeyFilename
	}
	rootCertPath := filepath.Join(opts.IssuerCredentialsPath, opts.X509.RootCAFilename)
	if filepath.IsAbs(opts.X509.RootCAFilename) {
		log.Debugf("Using user provided root cert path: %s", opts.X509.RootCAFilename)
		rootCertPath = opts.X509.RootCAFilename
	}
	jwtKeyPath := filepath.Join(opts.IssuerCredentialsPath, config.DefaultJWTSigningKeyFilename)
	if filepath.IsAbs(opts.JWT.SigningKeyFilename) {
		log.Debugf("Using user provided JWT signing key path: %s", opts.JWT.SigningKeyFilename)
		jwtKeyPath = opts.JWT.SigningKeyFilename
	}
	jwksPath := filepath.Join(opts.IssuerCredentialsPath, config.DefaultJWKSFilename)
	if filepath.IsAbs(opts.JWT.JWKSFilename) {
		log.Debugf("Using user provided JWKS path: %s", opts.JWT.JWKSFilename)
		jwksPath = opts.JWT.JWKSFilename
	}

	if opts.OIDC.TLSCertFile != nil && opts.OIDC.TLSKeyFile != nil {
		log.Info("Using OIDC TLS certificate and key files: %s, %s", *opts.OIDC.TLSCertFile, *opts.OIDC.TLSKeyFile)
	} else if opts.OIDC.TLSCertFile != nil || opts.OIDC.TLSKeyFile != nil {
		log.Fatal("both OIDC TLS certificate and key must be provided if one is specified")
	}

	m := make(map[string]struct{})
	// we need to watch over all these relevant directories
	for _, path := range []*string{
		&issuerCertPath,
		&issuerKeyPath,
		&rootCertPath,
		&jwtKeyPath,
		&jwksPath,
		opts.OIDC.TLSCertFile,
		opts.OIDC.TLSKeyFile,
	} {
		if path != nil {
			dir := filepath.Dir(*path)
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
	cfg.JWT.SigningKeyPath = jwtKeyPath
	cfg.JWT.Enabled = opts.JWT.Enabled
	cfg.JWT.JWKSPath = jwksPath
	cfg.JWT.SigningAlgorithm = opts.JWT.SigningAlgorithm
	cfg.TrustDomain = opts.TrustDomain
	cfg.Port = opts.Port
	cfg.ListenAddress = opts.ListenAddress
	cfg.Mode = modes.DaprMode(opts.Mode)

	if opts.JWT.Issuer != nil {
		cfg.JWT.Issuer = opts.JWT.Issuer
	}

	if opts.JWT.KeyID != nil {
		cfg.JWT.KeyID = opts.JWT.KeyID
	}

	cfg.JWT.TTL = opts.JWT.TTL

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

		sentry, serr := sentry.New(ctx, sentry.Options{
			Config:  cfg,
			Healthz: healthz,
			OIDC: sentry.OIDCOptions{
				Enabled:             opts.OIDC.Enabled,
				ServerListenAddress: opts.OIDC.ServerListenAddress,
				ServerListenPort:    opts.OIDC.ServerListenPort,
				JWKSURI:             opts.OIDC.JWKSURI,
				PathPrefix:          opts.OIDC.PathPrefix,
				Domains:             opts.OIDC.AllowedHosts,
				TLSCertPath:         opts.OIDC.TLSCertFile,
				TLSKeyPath:          opts.OIDC.TLSKeyFile,
			},
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
