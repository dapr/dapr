// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package operator

import (
	"context"

	v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	"github.com/dapr/dapr/pkg/credentials"
	"github.com/dapr/dapr/pkg/fswatcher"
	k8s "github.com/dapr/dapr/pkg/kubernetes"
	"github.com/dapr/dapr/pkg/logger"
	"github.com/dapr/dapr/pkg/operator/api"
	"github.com/dapr/dapr/pkg/operator/handlers"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

var log = logger.NewLogger("dapr.operator")

// Operator is an Dapr Kubernetes Operator for managing components and sidecar lifecycle
type Operator interface {
	Run(ctx context.Context)
}

type operator struct {
	kubeClient          kubernetes.Interface
	daprClient          scheme.Interface
	deploymentsInformer cache.SharedInformer
	componentsInformer  cache.SharedInformer
	ctx                 context.Context
	daprHandler         handlers.Handler
	apiServer           api.Server
	config              *Config
}

// NewOperator returns a new Dapr Operator
func NewOperator(kubeAPI *k8s.API, config *Config) Operator {
	kubeClient := kubeAPI.GetKubeClient()
	daprClient := kubeAPI.GetDaprClient()

	o := &operator{
		kubeClient: kubeClient,
		daprClient: daprClient,
		deploymentsInformer: k8s.DeploymentsIndexInformer(
			kubeClient,
			meta_v1.NamespaceAll,
			nil,
			nil,
		),
		componentsInformer: k8s.ComponentsIndexInformer(
			daprClient,
			meta_v1.NamespaceAll,
			nil,
			nil,
		),
		daprHandler: handlers.NewDaprHandler(kubeAPI),
		config:      config,
	}

	o.deploymentsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: o.syncDeployment,
		UpdateFunc: func(_, newObj interface{}) {
			o.syncComponent(newObj)
		},
		DeleteFunc: o.syncDeletedDeployment,
	})

	o.componentsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: o.syncComponent,
		UpdateFunc: func(_, newObj interface{}) {
			o.syncComponent(newObj)
		},
	})

	return o
}

func (o *operator) syncComponent(obj interface{}) {
	c, ok := obj.(*v1alpha1.Component)
	if ok {
		o.apiServer.OnComponentUpdated(c)
	}
}

func (o *operator) syncDeployment(obj interface{}) {
	o.daprHandler.ObjectCreated(obj)
}

func (o *operator) syncDeletedDeployment(obj interface{}) {
	o.daprHandler.ObjectDeleted(obj)
}

func (o *operator) Run(ctx context.Context) {
	defer runtime.HandleCrash()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	o.ctx = ctx
	go func() {
		<-ctx.Done()
		log.Infof("Dapr Operator is shutting down")
	}()
	log.Infof("Dapr Operator is started")
	go func() {
		o.deploymentsInformer.Run(ctx.Done())
		cancel()
	}()
	go func() {
		o.componentsInformer.Run(ctx.Done())
		cancel()
	}()

	o.apiServer = api.NewAPIServer(o.daprClient)

	var certChain *credentials.CertChain
	if o.config.MTLSEnabled {
		log.Info("mTLS enabled, getting tls certificates")
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
	}

	o.apiServer.Run(certChain)
	cancel()
}
