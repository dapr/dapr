/*
Copyright 2024 The Dapr Authors
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

package controller

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/cron"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.scheduler.server.controller")

type Options struct {
	KubeConfig *string
	Cron       cron.Interface
	Healthz    healthz.Healthz
}

func New(opts Options) (concurrency.Runner, error) {
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	var restConfig *rest.Config
	var err error
	if opts.KubeConfig != nil {
		var kcf []byte
		kcf, err = os.ReadFile(*opts.KubeConfig)
		if err != nil {
			return nil, fmt.Errorf("unable to read kubeconfig: %w", err)
		}

		restConfig, err = clientcmd.RESTConfigFromKubeConfig(kcf)
		if err != nil {
			return nil, fmt.Errorf("unable to create rest config from kubeconfig %q: %w", *opts.KubeConfig, err)
		}
	} else {
		restConfig, err = ctrl.GetConfig()
		if err != nil {
			return nil, err
		}
	}

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Logger:                        logr.Discard(),
		Scheme:                        scheme,
		HealthProbeBindAddress:        "0",
		Metrics:                       metricsserver.Options{BindAddress: "0"},
		LeaderElectionID:              "scheduler.dapr.io",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named("namespaces").
		For(new(corev1.Namespace)).
		Complete(&namespace{
			nsReader: mgr.GetCache(),
			cron:     opts.Cron,
		}); err != nil {
		return nil, fmt.Errorf("unable to complete controller: %w", err)
	}

	hzTarget := opts.Healthz.AddTarget()

	return concurrency.NewRunnerManager(
		mgr.Start,
		func(ctx context.Context) error {
			_, err := mgr.GetCache().GetInformer(ctx, new(corev1.Namespace))
			if err != nil {
				return fmt.Errorf("unable to get informer: %w", err)
			}
			if !mgr.GetCache().WaitForCacheSync(ctx) {
				return errors.New("unable to sync cache")
			}
			hzTarget.Ready()
			log.Info("Controller ready")
			<-ctx.Done()
			return nil
		},
	).Run, nil
}
