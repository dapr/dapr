/*
Copyright 2026 The Dapr Authors
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

package trustdistribution

import (
	"bytes"
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/dapr/dapr/pkg/security"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
	"github.com/dapr/kit/concurrency"
	"github.com/dapr/kit/logger"
)

var log = logger.NewLogger("dapr.operator.trustdistribution")

// Options are the options for the trust distribution Reconciler.
type Options struct {
	// Client is the cached controller-runtime client, used for Namespace
	// metadata reads and ConfigMap writes.
	Client client.Client

	// Reader is an uncached reader used to fetch ConfigMap contents without
	// holding every ConfigMap in the informer cache.
	Reader client.Reader

	// Security provides the current trust anchors and change notifications.
	Security security.Provider

	// ConfigMapName is the name of the trust anchors ConfigMap written into
	// every namespace.
	ConfigMapName string
}

// Reconciler distributes the Dapr trust anchors into a ConfigMap in every
// namespace so workloads can consume, and hot reload, the trust bundle from a
// mounted file. It reconciles namespaces on creation, repairs drift on the
// distributed ConfigMaps, and re-distributes to all namespaces whenever the
// trust anchors change.
type Reconciler struct {
	client        client.Client
	reader        client.Reader
	secProvider   security.Provider
	configMapName string

	eventCh chan event.GenericEvent
}

func New(opts Options) *Reconciler {
	name := opts.ConfigMapName
	if name == "" {
		name = securityConsts.TrustAnchorsConfigMapName
	}
	return &Reconciler{
		client:        opts.Client,
		reader:        opts.Reader,
		secProvider:   opts.Security,
		configMapName: name,
		eventCh:       make(chan event.GenericEvent, 1024),
	}
}

func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("trust-distribution").
		For(&corev1.Namespace{}, builder.OnlyMetadata).
		Watches(&corev1.ConfigMap{},
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: obj.GetNamespace()}}}
			}),
			builder.OnlyMetadata,
			builder.WithPredicates(predicate.NewPredicateFuncs(func(obj client.Object) bool {
				return obj.GetName() == r.configMapName
			})),
		).
		WatchesRawSource(source.Channel(r.eventCh,
			handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
				return []reconcile.Request{{NamespacedName: client.ObjectKey{Name: obj.GetName()}}}
			}),
		)).
		Complete(r)
}

// Start subscribes to trust anchor updates and enqueues every namespace for
// reconciliation when the anchors change. It implements
// manager.LeaderElectionRunnable so, like the controller itself, it only runs
// on the leader.
func (r *Reconciler) Start(ctx context.Context) error {
	sec, err := r.secProvider.Handler(ctx)
	if err != nil {
		return err
	}

	anchorsCh := make(chan []byte)

	return concurrency.NewRunnerManager(
		func(ctx context.Context) error {
			sec.WatchTrustAnchors(ctx, anchorsCh)
			return nil
		},
		func(ctx context.Context) error {
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-anchorsCh:
					log.Info("Trust anchors updated, re-distributing to all namespaces")
					if err := r.enqueueAllNamespaces(ctx); err != nil {
						log.Errorf("Failed to enqueue namespaces for trust anchor distribution: %s", err)
					}
				}
			}
		},
	).Run(ctx)
}

func (r *Reconciler) NeedLeaderElection() bool {
	return true
}

func (r *Reconciler) enqueueAllNamespaces(ctx context.Context) error {
	var namespaces metav1.PartialObjectMetadataList
	namespaces.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("NamespaceList"))
	if err := r.client.List(ctx, &namespaces); err != nil {
		return err
	}

	for i := range namespaces.Items {
		select {
		case r.eventCh <- event.GenericEvent{Object: &namespaces.Items[i]}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var namespace metav1.PartialObjectMetadata
	namespace.SetGroupVersionKind(corev1.SchemeGroupVersion.WithKind("Namespace"))
	err := r.client.Get(ctx, client.ObjectKey{Name: req.Name}, &namespace)
	if apierrors.IsNotFound(err) {
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if namespace.DeletionTimestamp != nil {
		return ctrl.Result{}, nil
	}

	sec, err := r.secProvider.Handler(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	anchors, err := sec.CurrentTrustAnchors(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}

	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      r.configMapName,
			Namespace: req.Name,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "dapr",
				"app.kubernetes.io/part-of":    "dapr",
				"app.kubernetes.io/managed-by": "dapr-operator",
			},
		},
		Data: map[string]string{
			securityConsts.TrustAnchorsConfigMapKey: string(anchors),
		},
	}

	var existing corev1.ConfigMap
	err = r.reader.Get(ctx, client.ObjectKeyFromObject(desired), &existing)
	if apierrors.IsNotFound(err) {
		if err = r.client.Create(ctx, desired); err != nil {
			if isNamespaceTerminating(err) {
				log.Debugf("Skipping trust anchors ConfigMap creation in terminating namespace %q", req.Name)
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("failed to create trust anchors ConfigMap in namespace %q: %w", req.Name, err)
		}
		log.Infof("Created trust anchors ConfigMap %q in namespace %q", r.configMapName, req.Name)
		return ctrl.Result{}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if configMapUpToDate(&existing, anchors) {
		return ctrl.Result{}, nil
	}

	existing.Data = desired.Data
	for k, v := range desired.Labels {
		if existing.Labels == nil {
			existing.Labels = make(map[string]string)
		}
		existing.Labels[k] = v
	}
	if err = r.client.Update(ctx, &existing); err != nil {
		if isNamespaceTerminating(err) || apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to update trust anchors ConfigMap in namespace %q: %w", req.Name, err)
	}
	log.Infof("Updated trust anchors ConfigMap %q in namespace %q", r.configMapName, req.Name)

	return ctrl.Result{}, nil
}

func configMapUpToDate(cm *corev1.ConfigMap, anchors []byte) bool {
	if len(cm.Data) != 1 {
		return false
	}
	return bytes.Equal([]byte(cm.Data[securityConsts.TrustAnchorsConfigMapKey]), anchors)
}

func isNamespaceTerminating(err error) bool {
	return apierrors.HasStatusCause(err, corev1.NamespaceTerminatingCause)
}
