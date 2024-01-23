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

package operator

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	argov1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	httpendpointsapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapiV1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorcache "github.com/dapr/dapr/pkg/operator/cache"
	"github.com/dapr/dapr/pkg/operator/handlers"
	operatorv1pb "github.com/dapr/dapr/pkg/proto/operator/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
)

var log = logger.NewLogger("dapr.operator")

// Operator is an Dapr Kubernetes Operator for managing components and sidecar lifecycle.
type Operator interface {
	Run(ctx context.Context) error
}

// Options contains the options for `NewOperator`.
type Options struct {
	Config                              string
	LeaderElection                      bool
	WatchdogEnabled                     bool
	WatchdogInterval                    time.Duration
	WatchdogMaxRestartsPerMin           int
	WatchNamespace                      string
	ServiceReconcilerEnabled            bool
	ArgoRolloutServiceReconcilerEnabled bool
	WatchdogCanPatchPodLabels           bool
	TrustAnchorsFile                    string
	APIPort                             int
	HealthzPort                         int
}

type operator struct {
	apiServer api.Server

	config *Config

	mgr         ctrl.Manager
	secProvider security.Provider
	healthzPort int
}

// NewOperator returns a new Dapr Operator.
func NewOperator(ctx context.Context, opts Options) (Operator, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("unable to get controller runtime configuration, err: %s", err)
	}

	config, err := LoadConfiguration(ctx, opts.Config, conf)
	if err != nil {
		return nil, fmt.Errorf("unable to load configuration, config: %s, err: %w", opts.Config, err)
	}

	secProvider, err := security.New(ctx, security.Options{
		SentryAddress:           config.SentryAddress,
		ControlPlaneTrustDomain: config.ControlPlaneTrustDomain,
		ControlPlaneNamespace:   security.CurrentNamespace(),
		TrustAnchorsFile:        opts.TrustAnchorsFile,
		AppID:                   "dapr-operator",
		// mTLS is always enabled for the operator.
		MTLSEnabled: true,
		Mode:        modes.KubernetesMode,
	})
	if err != nil {
		return nil, err
	}

	watchdogPodSelector := getSideCarInjectedNotExistsSelector()

	scheme, err := buildScheme(opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build operator scheme: %w", err)
	}

	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme: scheme,
		WebhookServer: webhook.NewServer(webhook.Options{
			Port: 19443,
			TLSOpts: []func(*tls.Config){
				func(tlsConfig *tls.Config) {
					sec, sErr := secProvider.Handler(ctx)
					// Error here means that the context has been cancelled before security
					// is ready.
					if sErr != nil {
						return
					}
					*tlsConfig = *sec.TLSServerConfigNoClientAuth()
				},
			},
		}),
		HealthProbeBindAddress: "0",
		Metrics: metricsserver.Options{
			BindAddress: "0",
		},
		LeaderElection:                opts.LeaderElection,
		LeaderElectionID:              "operator.dapr.io",
		NewCache:                      operatorcache.GetFilteredCache(opts.WatchNamespace, watchdogPodSelector),
		LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to start manager: %w", err)
	}
	mgrClient := mgr.GetClient()

	if opts.WatchdogEnabled {
		if !opts.LeaderElection {
			log.Warn("Leadership election is forcibly enabled when the Dapr Watchdog is enabled")
		}
		wd := &DaprWatchdog{
			client:            mgrClient,
			interval:          opts.WatchdogInterval,
			maxRestartsPerMin: opts.WatchdogMaxRestartsPerMin,
			canPatchPodLabels: opts.WatchdogCanPatchPodLabels,
			podSelector:       watchdogPodSelector,
		}
		if err := mgr.Add(wd); err != nil {
			return nil, fmt.Errorf("unable to add watchdog controller: %w", err)
		}
	} else {
		log.Infof("Dapr Watchdog is not enabled")
	}

	if opts.ServiceReconcilerEnabled {
		daprHandler := handlers.NewDaprHandlerWithOptions(mgr, &handlers.Options{ArgoRolloutServiceReconcilerEnabled: opts.ArgoRolloutServiceReconcilerEnabled})
		if err := daprHandler.Init(ctx); err != nil {
			return nil, fmt.Errorf("unable to initialize handler: %w", err)
		}
	}

	return &operator{
		mgr:         mgr,
		secProvider: secProvider,
		config:      config,
		healthzPort: opts.HealthzPort,
		apiServer: api.NewAPIServer(api.Options{
			Client:   mgrClient,
			Security: secProvider,
			Port:     opts.APIPort,
		}),
	}, nil
}

func (o *operator) syncComponent(ctx context.Context, eventType operatorv1pb.ResourceEventType) func(obj interface{}) {
	return func(obj interface{}) {
		var c *componentsapi.Component
		switch o := obj.(type) {
		case *componentsapi.Component:
			c = o
		case cache.DeletedFinalStateUnknown:
			c = o.Obj.(*componentsapi.Component)
		}
		if c != nil {
			log.Debugf("Observed component to be synced: %s/%s", c.Namespace, c.Name)
			o.apiServer.OnComponentUpdated(ctx, eventType, c)
		}
	}
}

func (o *operator) syncHTTPEndpoint(ctx context.Context) func(obj interface{}) {
	return func(obj interface{}) {
		e, ok := obj.(*httpendpointsapi.HTTPEndpoint)
		if ok {
			log.Debugf("Observed http endpoint to be synced: %s/%s", e.Namespace, e.Name)
			o.apiServer.OnHTTPEndpointUpdated(ctx, e)
		}
	}
}

func (o *operator) Run(ctx context.Context) error {
	log.Info("Dapr Operator is starting")
	healthzServer := health.NewServer(health.Options{
		Log:     log,
		Targets: ptr.Of(5),
	})

	/*
		Make sure to set `ENABLE_WEBHOOKS=false` when we run locally.
	*/
	enableConversionWebhooks := !strings.EqualFold(os.Getenv("ENABLE_WEBHOOKS"), "false")
	if enableConversionWebhooks {
		err := ctrl.NewWebhookManagedBy(o.mgr).
			For(&subscriptionsapiV1alpha1.Subscription{}).
			Complete()
		if err != nil {
			return fmt.Errorf("unable to create webhook Subscriptions v1alpha1: %w", err)
		}
		err = ctrl.NewWebhookManagedBy(o.mgr).
			For(&subscriptionsapiV2alpha1.Subscription{}).
			Complete()
		if err != nil {
			return fmt.Errorf("unable to create webhook Subscriptions v2alpha1: %w", err)
		}
	}

	caBundleCh := make(chan []byte)

	runner := concurrency.NewRunnerManager(
		o.secProvider.Run,
		func(ctx context.Context) error {
			// Wait for webhook certificates to be ready before starting the manager.
			_, rErr := o.secProvider.Handler(ctx)
			if rErr != nil {
				return rErr
			}
			healthzServer.Ready()
			return o.mgr.Start(ctx)
		},
		func(ctx context.Context) error {
			// start healthz server
			if rErr := healthzServer.Run(ctx, o.healthzPort); rErr != nil {
				return fmt.Errorf("failed to start healthz server: %w", rErr)
			}
			return nil
		},
		func(ctx context.Context) error {
			if rErr := o.apiServer.Ready(ctx); rErr != nil {
				return fmt.Errorf("API server did not become ready in time: %w", rErr)
			}
			healthzServer.Ready()
			log.Infof("Dapr Operator started")
			<-ctx.Done()
			return nil
		},
		func(ctx context.Context) error {
			if !enableConversionWebhooks {
				healthzServer.Ready()
				<-ctx.Done()
				return nil
			}

			sec, rErr := o.secProvider.Handler(ctx)
			if rErr != nil {
				return rErr
			}
			sec.WatchTrustAnchors(ctx, caBundleCh)
			return nil
		},
		func(ctx context.Context) error {
			if !enableConversionWebhooks {
				healthzServer.Ready()
				<-ctx.Done()
				return nil
			}

			sec, rErr := o.secProvider.Handler(ctx)
			if rErr != nil {
				return rErr
			}

			caBundle, rErr := sec.CurrentTrustAnchors()
			if rErr != nil {
				return rErr
			}

			for {
				rErr = o.patchConversionWebhooksInCRDs(ctx, caBundle, o.mgr.GetConfig(), "subscriptions.dapr.io")
				if rErr != nil {
					return rErr
				}

				healthzServer.Ready()

				select {
				case caBundle = <-caBundleCh:
				case <-ctx.Done():
					return nil
				}
			}
		},
		func(ctx context.Context) error {
			log.Info("Starting API server")
			rErr := o.apiServer.Run(ctx)
			if rErr != nil {
				return fmt.Errorf("failed to start API server: %w", rErr)
			}
			return nil
		},
		func(ctx context.Context) error {
			if !o.mgr.GetCache().WaitForCacheSync(ctx) {
				return errors.New("failed to wait for cache sync")
			}

			componentInformer, rErr := o.mgr.GetCache().GetInformer(ctx, &componentsapi.Component{})
			if rErr != nil {
				return fmt.Errorf("unable to get setup components informer: %w", rErr)
			}
			_, rErr = componentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: o.syncComponent(ctx, operatorv1pb.ResourceEventType_CREATED),
				UpdateFunc: func(_, newObj interface{}) {
					o.syncComponent(ctx, operatorv1pb.ResourceEventType_UPDATED)(newObj)
				},
				DeleteFunc: o.syncComponent(ctx, operatorv1pb.ResourceEventType_DELETED),
			})
			if rErr != nil {
				return fmt.Errorf("unable to add components informer event handler: %w", rErr)
			}
			healthzServer.Ready()
			<-ctx.Done()
			return nil
		},
		func(ctx context.Context) error {
			if !o.mgr.GetCache().WaitForCacheSync(ctx) {
				return errors.New("failed to wait for cache sync")
			}

			httpEndpointInformer, rErr := o.mgr.GetCache().GetInformer(ctx, &httpendpointsapi.HTTPEndpoint{})
			if rErr != nil {
				return fmt.Errorf("unable to get http endpoint informer: %w", rErr)
			}

			_, rErr = httpEndpointInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
				AddFunc: o.syncHTTPEndpoint(ctx),
				UpdateFunc: func(_, newObj interface{}) {
					o.syncHTTPEndpoint(ctx)(newObj)
				},
			})
			if rErr != nil {
				return fmt.Errorf("unable to add http endpoint informer event handler: %w", rErr)
			}
			healthzServer.Ready()
			<-ctx.Done()
			return nil
		},
	)

	return runner.Run(ctx)
}

// Patches the conversion webhooks in the specified CRDs to set the TLS configuration (including namespace and CA bundle)
func (o *operator) patchConversionWebhooksInCRDs(ctx context.Context, caBundle []byte, conf *rest.Config, crdNames ...string) error {
	clientSet, err := apiextensionsclient.NewForConfig(conf)
	if err != nil {
		return fmt.Errorf("could not get API extension client: %v", err)
	}

	crdClient := clientSet.ApiextensionsV1().CustomResourceDefinitions()

	for _, crdName := range crdNames {
		crd, err := crdClient.Get(ctx, crdName, v1.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not get CRD %q: %v", crdName, err)
		}

		if crd == nil ||
			crd.Spec.Conversion == nil ||
			crd.Spec.Conversion.Webhook == nil ||
			crd.Spec.Conversion.Webhook.ClientConfig == nil {
			return fmt.Errorf("crd %q does not have an existing webhook client config. Applying resources of this type will fail", crdName)
		}

		if crd.Spec.Conversion.Webhook.ClientConfig.Service != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace == security.CurrentNamespace() &&
			crd.Spec.Conversion.Webhook.ClientConfig.CABundle != nil &&
			bytes.Equal(crd.Spec.Conversion.Webhook.ClientConfig.CABundle, caBundle) {
			log.Infof("Conversion webhook for %q is up to date", crdName)

			continue
		}

		// This code mimics:
		// kubectl patch crd "subscriptions.dapr.io" --type='json' -p [{'op': 'replace', 'path': '/spec/conversion/webhook/clientConfig/service/namespace', 'value':'${namespace}'},{'op': 'add', 'path': '/spec/conversion/webhook/clientConfig/caBundle', 'value':'${caBundle}'}]"
		type patchValue struct {
			Op    string      `json:"op"`
			Path  string      `json:"path"`
			Value interface{} `json:"value"`
		}
		payload := []patchValue{{
			Op:    "replace",
			Path:  "/spec/conversion/webhook/clientConfig/service/namespace",
			Value: security.CurrentNamespace(),
		}, {
			Op:    "replace",
			Path:  "/spec/conversion/webhook/clientConfig/caBundle",
			Value: caBundle,
		}}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("could not marshal webhook spec: %w", err)
		}
		if _, err := crdClient.Patch(ctx, crdName, types.JSONPatchType, payloadJSON, v1.PatchOptions{}); err != nil {
			return fmt.Errorf("failed to patch webhook in CRD %q: %v", crdName, err)
		}

		log.Infof("Successfully patched webhook in CRD %q", crdName)
	}

	return nil
}

func buildScheme(opts Options) (*runtime.Scheme, error) {
	builders := []func(*runtime.Scheme) error{
		clientgoscheme.AddToScheme,
		componentsapi.AddToScheme,
		configurationapi.AddToScheme,
		resiliencyapi.AddToScheme,
		httpendpointsapi.AddToScheme,
		subscriptionsapiV1alpha1.AddToScheme,
		subscriptionsapiV2alpha1.AddToScheme,
	}

	if opts.ArgoRolloutServiceReconcilerEnabled {
		builders = append(builders, argov1alpha1.AddToScheme)
	}

	errs := make([]error, len(builders))
	scheme := runtime.NewScheme()
	for i, builder := range builders {
		errs[i] = builder(scheme)
	}

	return scheme, errors.Join(errs...)
}
