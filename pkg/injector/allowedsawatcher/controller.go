package allowedsawatcher

import (
	"context"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	injector "github.com/dapr/dapr/pkg/injector/interfaces"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.injector.allowedwatcher")

func NewWatcher(cfgServiceAccountNamesWatch string, injector injector.Injector, conf *rest.Config) ctrl.Manager {
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		MetricsBindAddress: "0", // disable metrics
	})
	if err != nil {
		log.Fatalf("Unable to get controller runtime manager, err: %s", err)
	}
	log.Info("Setting up allowed sa watcher controller")
	c, err := controller.New("allowed-sa-watcher", mgr, controller.Options{
		Reconciler: newAllowedSAWatcher(mgr.GetClient(), injector),
	})
	if err != nil {
		log.Fatalf("unable to set up allowed service account watcher, err: %s", err)
	}
	var preds []predicate.Predicate
	if cfgServiceAccountNamesWatch != "" {
		pred, watchErr := getNameNamespacePredicates(cfgServiceAccountNamesWatch)
		if watchErr != nil {
			log.Fatalf("problems getting namespace predicate setup, err: %s", err)
		}
		preds = append(preds, pred)
	}

	err = c.Watch(
		&source.Kind{Type: &corev1.ServiceAccount{}},
		&handler.EnqueueRequestForObject{},
		preds...)
	if err != nil {
		log.Fatalf("unable to watch dynamic Allowed Service Accounts, err: %s", err)
	}
	return mgr
}

// reconcileReplicaSet reconciles ReplicaSets
type allowedSAWatcher struct {
	// client can be used to retrieve objects from the APIServer.
	client              client.Client
	injector            injector.Injector
	namespaceNameToUIDs map[types.NamespacedName]string
	mu                  sync.RWMutex
}

func newAllowedSAWatcher(c client.Client, injector injector.Injector) *allowedSAWatcher {
	return &allowedSAWatcher{
		client:              c,
		injector:            injector,
		namespaceNameToUIDs: make(map[types.NamespacedName]string),
	}
}

// Implement reconcile.Reconciler so the controller can reconcile objects
var _ reconcile.Reconciler = &allowedSAWatcher{}

func (r *allowedSAWatcher) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	sa := &corev1.ServiceAccount{}
	err := r.client.Get(ctx, request.NamespacedName, sa)
	if err != nil {
		if errors.IsNotFound(err) {
			log.Debugf("service account %s in namespace %s was deleted", request.Name, request.Namespace)
			return r.processDelete(request)
		}
		return reconcile.Result{}, err
	}

	return r.processCreate(request.NamespacedName, sa)
}

func (r *allowedSAWatcher) processDelete(request reconcile.Request) (reconcile.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	uid, found := r.namespaceNameToUIDs[request.NamespacedName]
	if !found {
		log.Debugf("requested to delete UID for %s/%s but was not found", request.Name, request.Namespace)
		return reconcile.Result{}, nil
	}
	log.Debugf("deleting UID %s for %s/%s", uid, request.Name, request.Namespace)
	r.injector.UpdateAllowedAuthUIDs(nil, []string{uid})
	delete(r.namespaceNameToUIDs, request.NamespacedName)

	return reconcile.Result{}, nil
}

func (r *allowedSAWatcher) processCreate(nsName types.NamespacedName, sa *corev1.ServiceAccount) (reconcile.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	uid, found := r.namespaceNameToUIDs[nsName]

	if !found {
		uid = string(sa.GetUID())
		log.Debugf("requested to add UID %s for %s/%s", uid, nsName.Name, nsName.Namespace)
		r.injector.UpdateAllowedAuthUIDs([]string{uid}, nil)
		r.namespaceNameToUIDs[nsName] = uid
	}

	return reconcile.Result{}, nil
}
