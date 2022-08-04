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
	"context"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	runtimeutil "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
	resiliencyapi "github.com/dapr/dapr/pkg/apis/resiliency/v1alpha1"
	subscriptionsapi_v1alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	subscriptionsapi_v2alpha1 "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/fswatcher"
	"github.com/dapr/dapr/pkg/health"
	"github.com/dapr/dapr/pkg/operator/api"
	"github.com/dapr/dapr/pkg/operator/handlers"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator")

const (
	healthzPort = 8080
)

// Operator is an Dapr Kubernetes Operator for managing components and sidecar lifecycle.
type Operator interface {
	Run(ctx context.Context)
}

// Options contains the options for `NewOperator`.
type Options struct {
	Config                    string
	CertChainPath             string
	LeaderElection            bool
	WatchdogEnabled           bool
	WatchdogInterval          time.Duration
	WatchdogMaxRestartsPerMin int
}

type operator struct {
	daprHandler *handlers.DaprHandler
	apiServer   api.Server

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
	_ = subscriptionsapi_v1alpha1.AddToScheme(scheme)
	_ = subscriptionsapi_v2alpha1.AddToScheme(scheme)
}

// NewOperator returns a new Dapr Operator.
func NewOperator(opts Options) Operator {
	conf, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("unable to get controller runtime configuration, err: %s", err)
	}
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
		LeaderElection:     opts.LeaderElection,
		LeaderElectionID:   "operator.dapr.io",
	})
	if err != nil {
		log.Fatalf("unable to start manager, err: %s", err)
	}
	mgrClient := mgr.GetClient()

	wd := &DaprWatchdog{
		client:            mgrClient,
		enabled:           opts.WatchdogEnabled,
		interval:          opts.WatchdogInterval,
		maxRestartsPerMin: opts.WatchdogMaxRestartsPerMin,
	}
	err = mgr.Add(wd)
	if err != nil {
		log.Fatalf("unable to add watchdog controller, err: %s", err)
	}

	daprHandler := handlers.NewDaprHandler(mgr)
	err = daprHandler.Init()
	if err != nil {
		log.Fatalf("unable to initialize handler, err: %s", err)
	}

	o := &operator{
		daprHandler:   daprHandler,
		mgr:           mgr,
		client:        mgrClient,
		configName:    opts.Config,
		certChainPath: opts.CertChainPath,
	}
	o.apiServer = api.NewAPIServer(o.client)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	componentInformer, err := mgr.GetCache().GetInformer(ctx, &componentsapi.Component{})
	cancel()
	if err != nil {
		log.Fatalf("unable to get setup components informer, err: %s", err)
	} else {
		componentInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: o.syncComponent,
			UpdateFunc: func(_, newObj interface{}) {
				o.syncComponent(newObj)
			},
		})
	}

	return o
}

func (o *operator) prepareConfig() {
	var err error
	o.config, err = LoadConfiguration(o.configName, o.client)
	if err != nil {
		log.Fatalf("unable to load configuration, config: %s, err: %s", o.configName, err)
	}
	o.config.Credentials = credentials.NewTLSCredentials(o.certChainPath)
}

func (o *operator) syncComponent(obj interface{}) {
	c, ok := obj.(*componentsapi.Component)
	if ok {
		log.Debugf("observed component to be synced, %s/%s", c.Namespace, c.Name)
		o.apiServer.OnComponentUpdated(c)
	}
}

func (o *operator) loadCertChain(ctx context.Context) (certChain *credentials.CertChain) {
	log.Info("getting tls certificates")

	watchCtx, watchCancel := context.WithTimeout(ctx, time.Minute)
	fsevent := make(chan struct{})
	go func() {
		log.Infof("starting watch for certs on filesystem: %s", o.config.Credentials.Path())
		err := fswatcher.Watch(watchCtx, o.config.Credentials.Path(), fsevent)
		if err != nil {
			log.Fatalf("error starting watch on filesystem: %s", err)
		}
		close(fsevent)
		if watchCtx.Err() == context.DeadlineExceeded {
			log.Fatal("timeout while waiting to load tls certificates")
		}
	}()

	for {
		chain, err := credentials.LoadFromDisk(o.config.Credentials.RootCertPath(), o.config.Credentials.CertPath(), o.config.Credentials.KeyPath())
		if err == nil {
			log.Info("tls certificates loaded successfully")
			certChain = chain
			break
		}
		log.Infof("tls certificate not found; waiting for disk changes. err=%v", err)
		<-fsevent
		log.Debug("watcher found activity on filesystem")
	}

	watchCancel()

	return certChain
}

func (o *operator) Run(ctx context.Context) {
	defer runtimeutil.HandleCrash()

	log.Infof("Dapr Operator is starting")

	go func() {
		if err := o.mgr.Start(ctx); err != nil {
			log.Fatalf("failed to start controller manager, err: %s", err)
		}
	}()
	if !o.mgr.GetCache().WaitForCacheSync(ctx) {
		log.Fatalf("failed to wait for cache sync")
	}
	o.prepareConfig()

	// load certs from disk
	certChain := o.loadCertChain(ctx)

	// start healthz server
	healthzServer := health.NewServer(log)
	go func() {
		// blocking call
		err := healthzServer.Run(ctx, healthzPort)
		if err != nil {
			log.Fatalf("failed to start healthz server: %s", err)
		}
	}()

	// blocking call
	o.apiServer.Run(ctx, certChain, func() {
		healthzServer.Ready()
		log.Infof("Dapr Operator started")
	})

	log.Infof("Dapr Operator is shutting down")
}
