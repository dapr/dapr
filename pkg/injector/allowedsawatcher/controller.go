package allowedsawatcher

import (
	"context"
	injector "github.com/dapr/dapr/pkg/injector/interfaces"
	"github.com/dapr/kit/logger"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"sync"
)

var log = logger.NewLogger("dapr.injector.allowedwatcher")
var scheme = runtime.NewScheme()

func NewWatcher(cfgServiceAccountNamesWatch, cfgServiceAccountLabelsWatch string, injector injector.Injector) ctrl.Manager {
	// noop if nothing to watch
	if cfgServiceAccountNamesWatch == "" && cfgServiceAccountLabelsWatch == "" {
		return nil
	}
	conf, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("Unable to get controller runtime configuration, err: %s", err)
	}
	mgr, err := ctrl.NewManager(conf, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
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
	//if cfg.AllowedServiceAccountsWatchLabelSelector != "" {
	//	preds = append(preds, getLabelSelectorPredicate(cfg.AllowedServiceAccountsWatchLabelSelector))
	//}
	if cfgServiceAccountNamesWatch != "" {
		preds = append(preds, getNameNamespacePredicates(cfgServiceAccountNamesWatch))
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
		log.Debugf("requested to delete UID for %s/%s but was not found", request.Namespace, request.Name)
		return reconcile.Result{}, nil
	}
	r.injector.UpdateAllowedAuthUIDs(nil, []string{uid})
	delete(r.namespaceNameToUIDs, request.NamespacedName)

	return reconcile.Result{}, nil
}

func (r *allowedSAWatcher) processCreate(nsName types.NamespacedName, sa *corev1.ServiceAccount) (reconcile.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	uid, found := r.namespaceNameToUIDs[nsName]

	if !found {
		r.injector.UpdateAllowedAuthUIDs([]string{uid}, nil)
		r.namespaceNameToUIDs[nsName] = string(sa.GetUID())
	}

	return reconcile.Result{}, nil
}
