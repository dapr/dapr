// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package operator

import (
	"context"

	scheme "github.com/dapr/dapr/pkg/client/clientset/versioned"
	k8s "github.com/dapr/dapr/pkg/kubernetes"
	"github.com/dapr/dapr/pkg/operator/api"
	"github.com/dapr/dapr/pkg/operator/handlers"
	log "github.com/sirupsen/logrus"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

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
	componentsHandler   handlers.Handler
}

// NewOperator returns a new Dapr Operator
func NewOperator(kubeAPI *k8s.API) Operator {
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
		daprHandler:       handlers.NewDaprHandler(kubeAPI),
		componentsHandler: handlers.NewComponentsHandler(kubeClient),
	}

	o.deploymentsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: o.syncDeployment,
		UpdateFunc: func(_, newObj interface{}) {
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
	o.componentsHandler.ObjectCreated(obj)
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
	apiSrv := api.NewAPIServer(o.daprClient)
	apiSrv.Run(ctx)
	cancel()
}
