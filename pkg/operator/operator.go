package operator

import (
	"context"

	log "github.com/Sirupsen/logrus"
	scheme "github.com/actionscore/actions/pkg/client/clientset/versioned"
	k8s "github.com/actionscore/actions/pkg/kubernetes"
	"github.com/actionscore/actions/pkg/operator/api"
	"github.com/actionscore/actions/pkg/operator/handlers"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Operator is an Actions Kubernetes Operator for managing components and sidecar lifecycle
type Operator interface {
	Run(ctx context.Context)
}

type operator struct {
	kubeClient          kubernetes.Interface
	actionsClient       scheme.Interface
	deploymentsInformer cache.SharedInformer
	componentsInformer  cache.SharedInformer
	ctx                 context.Context
	actionsHandler      handlers.Handler
	componentsHandler   handlers.Handler
}

// NewOperator returns a new Actions Operator
func NewOperator(kubeClient kubernetes.Interface, actionsClient scheme.Interface) Operator {
	o := &operator{
		kubeClient:    kubeClient,
		actionsClient: actionsClient,
		deploymentsInformer: k8s.DeploymentsIndexInformer(
			kubeClient,
			meta_v1.NamespaceAll,
			nil,
			nil,
		),
		componentsInformer: k8s.ComponentsIndexInformer(
			actionsClient,
			meta_v1.NamespaceAll,
			nil,
			nil,
		),
		actionsHandler:    handlers.NewActionsHandler(actionsClient),
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
	o.actionsHandler.ObjectCreated(obj)
}

func (o *operator) syncDeletedDeployment(obj interface{}) {
	o.actionsHandler.ObjectDeleted(obj)
}

func (o *operator) Run(ctx context.Context) {
	defer runtime.HandleCrash()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	o.ctx = ctx
	go func() {
		<-ctx.Done()
		log.Infof("Actions Operator is shutting down")
	}()
	log.Infof("Actions Operator is started")
	go func() {
		o.deploymentsInformer.Run(ctx.Done())
		cancel()
	}()
	go func() {
		o.componentsInformer.Run(ctx.Done())
		cancel()
	}()
	apiSrv := api.NewAPIServer(o.actionsClient)
	apiSrv.Run(ctx)
	cancel()
}
