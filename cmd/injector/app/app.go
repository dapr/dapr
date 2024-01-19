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
	"encoding/base64"
	"fmt"
	"os"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"github.com/dapr/dapr/cmd/injector/options"
	"github.com/dapr/dapr/pkg/buildinfo"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/injector/sentry"
	"github.com/dapr/dapr/pkg/injector/service"
	"github.com/dapr/dapr/pkg/metrics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/dapr/utils"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/signals"
)

var log = logger.NewLogger("dapr.injector")

func Run() {
	opts := options.New(os.Args[1:])

	// Apply options to all loggers
	err := logger.ApplyOptionsToLoggers(&opts.Logger)
	if err != nil {
		log.Fatal(err)
	}

	log.Infof("Starting Dapr Sidecar Injector -- version %s -- commit %s", buildinfo.Version(), buildinfo.Commit())
	log.Infof("Log level set to: %s", opts.Logger.OutputLevel)

	metricsExporter := metrics.NewExporterWithOptions(log, metrics.DefaultMetricNamespace, opts.Metrics)

	err = utils.SetEnvVariables(map[string]string{
		utils.KubeConfigVar: opts.Kubeconfig,
	})
	if err != nil {
		log.Fatalf("Error set env: %v", err)
	}

	// Initialize injector service metrics
	err = service.InitMetrics()
	if err != nil {
		log.Fatal(err)
	}

	ctx := signals.Context()
	cfg, err := service.GetConfig()
	if err != nil {
		log.Fatalf("Error getting config: %v", err)
	}

	kubeClient := utils.GetKubeClient()
	conf := utils.GetConfig()
	daprClient, err := scheme.NewForConfig(conf)
	if err != nil {
		log.Fatalf("Error creating Dapr client: %v", err)
	}
	uids, err := service.AllowedControllersServiceAccountUID(ctx, cfg, kubeClient)
	if err != nil {
		log.Fatalf("Failed to get authentication uids from services accounts: %s", err)
	}

	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           cfg.SentryAddress,
		ControlPlaneTrustDomain: cfg.ControlPlaneTrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        cfg.TrustAnchorsFile,
		AppID:                   "dapr-injector",
		MTLSEnabled:             true,
		Mode:                    modes.KubernetesMode,
	})
	if err != nil {
		log.Fatal(err)
	}

	inj, err := service.NewInjector(service.Options{
		AuthUIDs:                uids,
		Config:                  cfg,
		DaprClient:              daprClient,
		KubeClient:              kubeClient,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		ControlPlaneTrustDomain: cfg.ControlPlaneTrustDomain,
	})
	if err != nil {
		log.Fatalf("Error creating injector: %v", err)
	}

	healthzServer := health.NewServer(health.Options{Log: log})
	caBundleCh := make(chan []byte)
	mngr := concurrency.NewRunnerManager(
		metricsExporter.Run,
		secProvider.Run,
		func(ctx context.Context) error {
			sec, rerr := secProvider.Handler(ctx)
			if rerr != nil {
				return rerr
			}
			sentryID, rerr := security.SentryID(sec.ControlPlaneTrustDomain(), security.CurrentNamespace())
			if err != nil {
				return rerr
			}
			requester := sentry.New(sentry.Options{
				SentryAddress: cfg.SentryAddress,
				SentryID:      sentryID,
				Security:      sec,
			})
			return inj.Run(ctx,
				sec.TLSServerConfigNoClientAuth(),
				sentryID,
				requester.RequestCertificateFromSentry,
				sec.CurrentTrustAnchors,
			)
		},
		func(ctx context.Context) error {
			readyErr := inj.Ready(ctx)
			if readyErr != nil {
				return readyErr
			}
			healthzServer.Ready()
			<-ctx.Done()
			return nil
		},
		func(ctx context.Context) error {
			healhtzErr := healthzServer.Run(ctx, opts.HealthzPort)
			if healhtzErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", healhtzErr)
			}
			return nil
		},
		func(ctx context.Context) error {
			sec, rErr := secProvider.Handler(ctx)
			if rErr != nil {
				return rErr
			}
			sec.WatchTrustAnchors(ctx, caBundleCh)
			return nil
		},
		// Watch for changes to the trust anchors and update the webhook
		// configuration on events.
		func(ctx context.Context) error {
			sec, rerr := secProvider.Handler(ctx)
			if rerr != nil {
				return rerr
			}

			caBundle, rErr := sec.CurrentTrustAnchors()
			if rErr != nil {
				return rErr
			}

			// Patch the mutating webhook configuration with the current trust
			// anchors.
			// Re-patch every time the trust anchors change.
			for {
				_, rErr = kubeClient.AdmissionregistrationV1().MutatingWebhookConfigurations().Patch(ctx,
					"dapr-sidecar-injector",
					types.JSONPatchType,
					[]byte(`[{"op":"replace","path":"/webhooks/0/clientConfig/caBundle","value":"`+base64.StdEncoding.EncodeToString(caBundle)+`"}]`),
					metav1.PatchOptions{},
				)
				if rErr != nil {
					return rErr
				}

				select {
				case caBundle = <-caBundleCh:
				case <-ctx.Done():
					return nil
				}
			}
		},
	)

	err = mngr.Run(ctx)
	if err != nil {
		log.Fatalf("Error running injector: %v", err)
	}

	log.Info("Dapr sidecar injector shut down gracefully")
}
