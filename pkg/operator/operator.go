// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation and Dapr Contributors.
// Licensed under the MIT License.
// ------------------------------------------------------------

package operator

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	runtimeutil "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	configurationapi "github.com/dapr/dapr/pkg/apis/configuration/v1alpha1"
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

type operator struct {
	ctx         context.Context
	daprHandler handlers.Handler
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
	_ = subscriptionsapi_v1alpha1.AddToScheme(scheme)
	_ = subscriptionsapi_v2alpha1.AddToScheme(scheme)
}

// NewOperator returns a new Dapr Operator.
func NewOperator(config, certChainPath string, enableLeaderElection bool) Operator {
	conf, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("unable to get controller runtime configuration, err: %s", err)
	}
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "operator.dapr.io",
	})
	if err != nil {
		log.Fatal("unable to start manager")
	}
	daprHandler := handlers.NewDaprHandler(mgr)
	if err := daprHandler.Init(); err != nil {
		log.Fatalf("unable to initialize handler, err: %s", err)
	}

	o := &operator{
		daprHandler:   daprHandler,
		mgr:           mgr,
		client:        mgr.GetClient(),
		configName:    config,
		certChainPath: certChainPath,
	}
	o.apiServer = api.NewAPIServer(o.client)
	if componentInformer, err := mgr.GetCache().GetInformer(context.TODO(), &componentsapi.Component{}); err != nil {
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

func (o *operator) Run(ctx context.Context) {
	defer runtimeutil.HandleCrash()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	o.ctx = ctx
	go func() {
		<-ctx.Done()
		log.Infof("Dapr Operator is shutting down")
	}()
	log.Infof("Dapr Operator is started")

	go func() {
		if err := o.mgr.Start(ctx); err != nil {
			if err != nil {
				log.Fatalf("failed to start controller manager, err: %s", err)
			}
		}
	}()
	if !o.mgr.GetCache().WaitForCacheSync(ctx) {
		log.Fatalf("failed to wait for cache sync")
	}
	o.prepareConfig()

	var certChain *credentials.CertChain
	log.Info("getting tls certificates")
	// try to load certs from disk, if not yet there, start a watch on the local filesystem
	chain, err := credentials.LoadFromDisk(o.config.Credentials.RootCertPath(), o.config.Credentials.CertPath(), o.config.Credentials.KeyPath())
	if err != nil {
		fsevent := make(chan struct{})

		go func() {
			log.Infof("starting watch for certs on filesystem: %s", o.config.Credentials.Path())
			err = fswatcher.Watch(ctx, o.config.Credentials.Path(), fsevent)
			if err != nil {
				log.Fatal("error starting watch on filesystem: %s", err)
			}
		}()

		<-fsevent
		log.Info("certificates detected")

		chain, err = credentials.LoadFromDisk(o.config.Credentials.RootCertPath(), o.config.Credentials.CertPath(), o.config.Credentials.KeyPath())
		if err != nil {
			log.Fatal("failed to load cert chain from disk: %s", err)
		}
	}
	certChain = chain
	log.Info("tls certificates loaded successfully")

	go func() {
		healthzServer := health.NewServer(log)
		healthzServer.Ready()

		err := healthzServer.Run(ctx, healthzPort)
		if err != nil {
			log.Fatalf("failed to start healthz server: %s", err)
		}
	}()

	o.apiServer.Run(certChain)
}
