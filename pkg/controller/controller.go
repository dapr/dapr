package controller

import (
	"context"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/api"
	scheme "github.com/actionscore/actions/pkg/client/clientset/versioned"
	"github.com/actionscore/actions/pkg/handlers"
	k8s "github.com/actionscore/actions/pkg/kubernetes"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Controller interface {
	Run(ctx context.Context)
}

type controller struct {
	KubeClient           kubernetes.Interface
	ActionsClient        scheme.Interface
	DeploymentsInformer  cache.SharedInformer
	EventSourcesInformer cache.SharedInformer
	Ctx                  context.Context
	ActionsHandler       handlers.Handler
	EventSourcesHandler  handlers.Handler
}

func NewController(kubeClient kubernetes.Interface, actionsClient scheme.Interface, config Config) *controller {
	c := &controller{
		KubeClient:    kubeClient,
		ActionsClient: actionsClient,
		DeploymentsInformer: k8s.DeploymentsIndexInformer(
			kubeClient,
			meta_v1.NamespaceAll,
			nil,
			nil,
		),
		EventSourcesInformer: k8s.EventSourcesIndexInformer(
			actionsClient,
			meta_v1.NamespaceAll,
			nil,
			nil,
		),
		ActionsHandler:      handlers.NewActionsHandler(actionsClient, handlers.ActionsHandlerConfig{RuntimeImage: config.ActionsRuntimeImage, ImagePullSecretName: config.ImagePullSecretName}),
		EventSourcesHandler: handlers.NewEventSourcesHandler(kubeClient),
	}

	c.DeploymentsInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.syncDeployment,
		UpdateFunc: func(_, newObj interface{}) {
			c.syncDeployment(newObj)
		},
		DeleteFunc: c.syncDeletedDeployment,
	})

	c.EventSourcesInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: c.syncEventSource,
		UpdateFunc: func(_, newObj interface{}) {
			c.syncEventSource(newObj)
		},
	})

	return c
}

func (c *controller) syncEventSource(obj interface{}) {
	c.EventSourcesHandler.ObjectCreated(obj)
}

func (c *controller) syncDeployment(obj interface{}) {
	c.ActionsHandler.ObjectCreated(obj)
}

func (c *controller) syncDeletedDeployment(obj interface{}) {
	c.ActionsHandler.ObjectDeleted(obj)
}

func (c *controller) Run(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.Ctx = ctx
	go func() {
		<-ctx.Done()
		log.Infof("Controller is shutting down")
	}()
	log.Infof("Controller is started")
	go func() {
		c.DeploymentsInformer.Run(ctx.Done())
		cancel()
	}()
	go func() {
		c.EventSourcesInformer.Run(ctx.Done())
		cancel()
	}()
	apiSrv := api.NewAPIServer(c.ActionsClient)
	apiSrv.Run(ctx)
	cancel()
}
