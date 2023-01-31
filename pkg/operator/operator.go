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
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapiV1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapiV2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/operator/api"
	operatorcache "github.com/dapr/dapr/pkg/operator/cache"
	"github.com/dapr/dapr/pkg/operator/handlers"
	"github.com/dapr/kit/fswatcher"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator")

const (
	healthzPort = 8080
)

// Operator is an Dapr Kubernetes Operator for managing components and sidecar lifecycle.
type Operator interface {
	Run(ctx context.Context) error
}

// Options contains the options for `NewOperator`.
type Options struct {
	Config                              string
	CertChainPath                       string
	LeaderElection                      bool
	WatchdogEnabled                     bool
	WatchdogInterval                    time.Duration
	WatchdogMaxRestartsPerMin           int
	WatchNamespace                      string
	ServiceReconcilerEnabled            bool
	ArgoRolloutServiceReconcilerEnabled bool
	WatchdogCanPatchPodLabels           bool
}

type operator struct {
	apiServer api.Server

	configName    string
	certChainPath string
	config        *Config

	mgr    ctrl.Manager
	client client.Client
}

var scheme = runtime.NewScheme()

func init() {
	_ = clientgoscheme.AddToScheme(scheme)

	_ = componentsapi.AddToScheme(scheme)
	_ = configurationapi.AddToScheme(scheme)
	_ = resiliencyapi.AddToScheme(scheme)
	_ = subscriptionsapiV1alpha1.AddToScheme(scheme)
	_ = subscriptionsapiV2alpha1.AddToScheme(scheme)
}

// NewOperator returns a new Dapr Operator.
func NewOperator(ctx context.Context, opts Options) (Operator, error) {
	conf, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("Unable to get controller runtime configuration, err: %s", err)
	}
	watchdogPodSelector := getSideCarInjectedNotExistsSelector()
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:                 scheme,
		Port:                   19443,
		HealthProbeBindAddress: "0",
		MetricsBindAddress:     "0",
		LeaderElection:         opts.LeaderElection,
		LeaderElectionID:       "operator.dapr.io",
		Namespace:              opts.WatchNamespace,
		NewCache:               operatorcache.GetFilteredCache(watchdogPodSelector),
	})
	if err != nil {
		return nil, fmt.Errorf("Unable to start manager, err: %s", err)
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
			return nil, fmt.Errorf("Unable to add watchdog controller, err: %s", err)
		}
	} else {
		log.Infof("Dapr Watchdog is not enabled")
	}

	if opts.ServiceReconcilerEnabled {
		daprHandler := handlers.NewDaprHandlerWithOptions(mgr, &handlers.Options{ArgoRolloutServiceReconcilerEnabled: opts.ArgoRolloutServiceReconcilerEnabled})
		if err := daprHandler.Init(ctx); err != nil {
			return nil, fmt.Errorf("Unable to initialize handler, err: %s", err)
		}
	}

	o := &operator{
		mgr:           mgr,
		client:        mgrClient,
		configName:    opts.Config,
		certChainPath: opts.CertChainPath,
	}
	o.apiServer = api.NewAPIServer(o.client)

	return o, nil
}

func (o *operator) prepareConfig() error {
	var err error
	o.config, err = LoadConfiguration(o.configName, o.client)
	if err != nil {
		return fmt.Errorf("Unable to load configuration, config: %s, err: %s", o.configName, err)
	}
	o.config.Credentials = credentials.NewTLSCredentials(o.certChainPath)
	return nil
}

func (o *operator) syncComponent(ctx context.Context) func(obj interface{}) {
	return func(obj interface{}) {
		c, ok := obj.(*componentsapi.Component)
		if ok {
			log.Debugf("Observed component to be synced, %s/%s", c.Namespace, c.Name)
			o.apiServer.OnComponentUpdated(ctx, c)
		}
	}
}

func (o *operator) loadCertChain(ctx context.Context) (*credentials.CertChain, error) {
	log.Info("Getting TLS certificates")

	watchCtx, watchCancel := context.WithTimeout(ctx, time.Minute)
	fsevent := make(chan struct{})
	fserr := make(chan error)

	go func() {
		log.Infof("Starting watch for certs on filesystem: %s", o.config.Credentials.Path())
		err := fswatcher.Watch(watchCtx, o.config.Credentials.Path(), fsevent)
		// Watch always returns an error, which is context.Canceled if everything went well
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				// Ignore context.Canceled
				fserr <- fmt.Errorf("Error starting watch on filesystem: %s", err)
			} else {
				fserr <- nil
			}

			return
		}

		close(fsevent)
	}()

	var certChain *credentials.CertChain
	for {
		chain, err := credentials.LoadFromDisk(o.config.Credentials.RootCertPath(), o.config.Credentials.CertPath(), o.config.Credentials.KeyPath())
		if err == nil {
			log.Info("TLS certificates loaded successfully")
			watchCancel()
			certChain = chain
			break
		}
		log.Infof("TLS certificate not found; waiting for disk changes. err=%v", err)
		select {
		case <-fsevent:
			log.Debug("Watcher found activity on filesystem")
			continue
		case <-watchCtx.Done():
			return nil, errors.New("timeout while waiting to load TLS certificates")
		}
	}

	return certChain, <-fserr
}

func (o *operator) Run(ctx context.Context) error {
	log.Infof("Dapr Operator is starting")
	healthzServer := health.NewServer(log)

	if err := o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		// start healthz server
		if err := healthzServer.Run(ctx, healthzPort); err != nil {
			return fmt.Errorf("Failed to start healthz server: %s", err)
		}
		return nil
	}}); err != nil {
		return err
	}

	if err := o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		if err := o.apiServer.Ready(ctx); err != nil {
			return fmt.Errorf("Failed to start API server: %s", err)
		}
		healthzServer.Ready()
		log.Infof("Dapr Operator started")
		<-ctx.Done()
		return nil
	}}); err != nil {
		return err
	}

	if err := o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		if err := o.prepareConfig(); err != nil {
			return err
		}

		/*
			Make sure to set `ENABLE_WEBHOOKS=false` when we run locally.
		*/
		if !strings.EqualFold(os.Getenv("ENABLE_WEBHOOKS"), "false") {
			if err := ctrl.NewWebhookManagedBy(o.mgr).
				For(&subscriptionsapiV1alpha1.Subscription{}).
				Complete(); err != nil {
				return fmt.Errorf("unable to create webhook Subscriptions v1alpha1: %v", err)
			}
			if err := ctrl.NewWebhookManagedBy(o.mgr).
				For(&subscriptionsapiV2alpha1.Subscription{}).
				Complete(); err != nil {
				return fmt.Errorf("unable to create webhook Subscriptions v2alpha1: %v", err)
			}
		}

		// load certs from disk
		certChain, err := o.loadCertChain(ctx)
		if err != nil {
			return fmt.Errorf("failed to load cert chain: %s", err)
		}

		if err := o.patchCRDs(ctx, certChain.RootCA, o.mgr.GetConfig(), "subscriptions.dapr.io"); err != nil {
			return err
		}

		log.Info("starting api server")
		if err := o.apiServer.Run(ctx, certChain); err != nil {
			return fmt.Errorf("Failed to start API server: %s", err)
		}
		return nil
	}}); err != nil {
		return err
	}

	if err := o.mgr.Add(nonLeaderRunnable{func(ctx context.Context) error {
		if !o.mgr.GetCache().WaitForCacheSync(ctx) {
			return errors.New("failed to wait for cache sync")
		}

		componentInformer, err := o.mgr.GetCache().GetInformer(ctx, &componentsapi.Component{})
		if err != nil {
			return fmt.Errorf("Unable to get setup components informer, err: %s", err)
		}

		if _, err := componentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: o.syncComponent(ctx),
			UpdateFunc: func(_, newObj interface{}) {
				o.syncComponent(ctx)(newObj)
			},
		}); err != nil {
			return fmt.Errorf("Unable to add components informer event handler, err: %s", err)
		}
		<-ctx.Done()
		return nil
	}}); err != nil {
		return err
	}

	if err := o.mgr.Start(ctx); err != nil {
		return fmt.Errorf("error running operator: %s", err)
	}

	return nil
}

func (o *operator) patchCRDs(ctx context.Context, trustChain []byte, conf *rest.Config, crdNames ...string) error {
	clientSet, err := apiextensionsclient.NewForConfig(conf)
	if err != nil {
		return fmt.Errorf("Could not get API extension client: %v", err)
	}

	crdClient := clientSet.ApiextensionsV1().CustomResourceDefinitions()
	namespace := os.Getenv("NAMESPACE")
	if namespace == "" {
		return errors.New("Could not get dapr namespace")
	}

	for _, crdName := range crdNames {
		crd, err := crdClient.Get(ctx, crdName, v1.GetOptions{})
		if err != nil {
			return fmt.Errorf("could not get CRD %q: %v", crdName, err)
		}

		if crd == nil ||
			crd.Spec.Conversion == nil ||
			crd.Spec.Conversion.Webhook == nil ||
			crd.Spec.Conversion.Webhook.ClientConfig == nil {
			return fmt.Errorf("CRD %q does not have an existing webhook client config. Applying resources of this type will fail.", crdName)
		}

		if crd.Spec.Conversion.Webhook.ClientConfig.Service != nil &&
			crd.Spec.Conversion.Webhook.ClientConfig.Service.Namespace == namespace &&
			crd.Spec.Conversion.Webhook.ClientConfig.CABundle != nil &&
			bytes.Equal(crd.Spec.Conversion.Webhook.ClientConfig.CABundle, trustChain) {
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
			Value: namespace,
		}, {
			Op:    "replace",
			Path:  "/spec/conversion/webhook/clientConfig/caBundle",
			Value: trustChain,
		}}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("Could not marshal webhook spec: %v", err)
		}
		if _, err := crdClient.Patch(ctx, crdName, types.JSONPatchType, payloadJSON, v1.PatchOptions{}); err != nil {
			return fmt.Errorf("Failed to patch webhook in CRD %q: %v", crdName, err)
		}

		log.Infof("Successfully patched webhook in CRD %q", crdName)
	}

	return nil
}

type nonLeaderRunnable struct {
	fn func(ctx context.Context) error
}

func (r nonLeaderRunnable) Start(ctx context.Context) error {
	return r.fn(ctx)
}

func (r nonLeaderRunnable) NeedLeaderElection() bool {
	return false
}
