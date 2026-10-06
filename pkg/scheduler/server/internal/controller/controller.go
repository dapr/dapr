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
	"sync"
	"sync/atomic"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/dapr/dapr/pkg/healthz"
	"github.com/dapr/dapr/pkg/scheduler/server/internal/cron"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var placementPodLabels = labels.Set{"app": "dapr-placement-server"}

var log = logger.NewLogger("dapr.scheduler.server.controller")

type Options struct {
	KubeConfig *string
	Healthz    healthz.Healthz
	Namespace  string
}

// Controller wraps a long-lived controller-runtime manager. It must only be
// created once per process because controller-runtime registers controller
// names globally in the metrics registry.
type Controller struct {
	cron atomic.Value
	run  func(ctx context.Context) error

	lock                 sync.Mutex
	sink                 PresenceSink
	placementPodPresence *bool
}

// PresenceSink receives the placement pod observations.
type PresenceSink interface {
	SetKubernetesPresence(present bool)
}

// SetCron updates the cron interface used by the namespace reconciler.
func (c *Controller) SetCron(cr cron.Interface) {
	c.cron.Store(cr)
}

// SetPresenceSink updates the sink receiving placement pod observations,
// pushing the current observation into it right away.
func (c *Controller) SetPresenceSink(s PresenceSink) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.sink = s
	if c.placementPodPresence != nil {
		s.SetKubernetesPresence(*c.placementPodPresence)
	}
}

func (c *Controller) setPlacementPresence(present bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.placementPodPresence = &present
	if c.sink != nil {
		c.sink.SetKubernetesPresence(present)
	}
}

func New(opts Options) (*Controller, error) {
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
		Logger:                 logr.Discard(),
		Scheme:                 scheme,
		HealthProbeBindAddress: "0",
		Metrics:                metricsserver.Options{BindAddress: "0"},
		Cache: cache.Options{
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Pod{}: {
					Namespaces: map[string]cache.Config{
						opts.Namespace: {
							LabelSelector: placementPodLabels.AsSelector(),
						},
					},
				},
			},
		},
		LeaderElectionID:              "scheduler.dapr.io",
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}

	c := new(Controller)

	if err := ctrl.NewControllerManagedBy(mgr).
		Named("namespaces").
		For(new(corev1.Namespace)).
		Complete(&namespace{
			nsReader: mgr.GetCache(),
			ctrl:     c,
		}); err != nil {
		return nil, fmt.Errorf("unable to complete controller: %w", err)
	}

	if err := ctrl.NewControllerManagedBy(mgr).
		Named("placement-pods").
		For(new(corev1.Pod)).
		Complete(&placementPods{
			podReader: mgr.GetCache(),
			ctrl:      c,
			namespace: opts.Namespace,
		}); err != nil {
		return nil, fmt.Errorf("unable to complete controller: %w", err)
	}

	hzTarget := opts.Healthz.AddTarget("scheduler-controller")

	c.run = concurrency.NewRunnerManager(
		mgr.Start,
		func(ctx context.Context) error {
			for _, obj := range []client.Object{new(corev1.Namespace), new(corev1.Pod)} {
				if _, err := mgr.GetCache().GetInformer(ctx, obj); err != nil {
					return fmt.Errorf("unable to get informer: %w", err)
				}
			}
			if !mgr.GetCache().WaitForCacheSync(ctx) {
				return errors.New("unable to sync cache")
			}
			hzTarget.Ready()
			log.Info("Controller ready")
			<-ctx.Done()
			return nil
		},
	).Run

	return c, nil
}

func (c *Controller) Run(ctx context.Context) error {
	return c.run(ctx)
}
