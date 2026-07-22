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

package operator

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	injectorConsts "github.com/dapr/dapr/pkg/injector/consts"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
)

// trustBundleSyncInterval is how often the trust bundle ConfigMap is synced
// into Dapr-enabled namespaces. It must be well below sentry's rotation
// propagation window (default 24h) so rotated trust anchors reach every
// namespace long before signing switches.
const trustBundleSyncInterval = 30 * time.Second

// TrustBundleSync periodically copies the dapr-trust-bundle ConfigMap from
// the control-plane namespace into every namespace running Dapr-enabled
// workloads. Pods mount this ConfigMap as their trust anchor source, so the
// synced copies are how root CA updates (e.g. during rotation) reach
// workloads; sentry also verifies these copies carry the new root CA before
// the rotation cleanup removes the old one.
type TrustBundleSync struct {
	client                kubernetes.Interface
	controlPlaneNamespace string
	interval              time.Duration
}

// NeedLeaderElection makes the sync run only on the elected leader, like the
// watchdog.
func (s *TrustBundleSync) NeedLeaderElection() bool {
	return true
}

func (s *TrustBundleSync) Start(ctx context.Context) error {
	log.Infof("TrustBundleSync started, syncing every %s", s.interval)

	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()

	for {
		if err := s.sync(ctx); err != nil {
			log.Errorf("Failed to sync trust bundle ConfigMap: %v", err)
		}

		select {
		case <-ctx.Done():
			log.Info("TrustBundleSync is shutting down")
			return nil
		case <-ticker.C:
		}
	}
}

// sync copies the source trust bundle ConfigMap into every namespace that
// runs Dapr-enabled workloads.
func (s *TrustBundleSync) sync(ctx context.Context) error {
	source, err := s.client.CoreV1().ConfigMaps(s.controlPlaneNamespace).Get(ctx, securityConsts.TrustBundleK8sSecretName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			// Nothing to sync yet; sentry creates the source ConfigMap.
			return nil
		}
		return fmt.Errorf("failed to get source trust bundle configmap: %w", err)
	}

	pods, err := s.client.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
		LabelSelector: injectorConsts.SidecarInjectedLabel + "=true",
	})
	if err != nil {
		return fmt.Errorf("failed to list Dapr-enabled pods: %w", err)
	}

	namespaces := make(map[string]struct{})
	for i := range pods.Items {
		if ns := pods.Items[i].Namespace; ns != s.controlPlaneNamespace {
			namespaces[ns] = struct{}{}
		}
	}

	var errs []error
	for namespace := range namespaces {
		if err := s.syncNamespace(ctx, namespace, source); err != nil {
			errs = append(errs, fmt.Errorf("namespace %s: %w", namespace, err))
		}
	}
	return errors.Join(errs...)
}

func (s *TrustBundleSync) syncNamespace(ctx context.Context, namespace string, source *corev1.ConfigMap) error {
	existing, err := s.client.CoreV1().ConfigMaps(namespace).Get(ctx, source.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		_, err = s.client.CoreV1().ConfigMaps(namespace).Create(ctx, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      source.Name,
				Namespace: namespace,
			},
			Data: source.Data,
		}, metav1.CreateOptions{})
		if err == nil {
			log.Infof("Created trust bundle ConfigMap in namespace %s", namespace)
		}
		return err
	}
	if err != nil {
		return err
	}

	if maps.Equal(existing.Data, source.Data) {
		return nil
	}

	existing.Data = source.Data
	if _, err = s.client.CoreV1().ConfigMaps(namespace).Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return err
	}
	log.Infof("Updated trust bundle ConfigMap in namespace %s", namespace)
	return nil
}
