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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTrustBundleSync(t *testing.T) {
	sourceCM := func(data map[string]string) *corev1.ConfigMap {
		return &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "dapr-trust-bundle", Namespace: "dapr-system"},
			Data:       data,
		}
	}
	daprPod := func(namespace, name string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    map[string]string{"dapr.io/sidecar-injected": "true"},
			},
		}
	}
	newSync := func(objects ...runtime.Object) *TrustBundleSync {
		return &TrustBundleSync{
			client:                fake.NewSimpleClientset(objects...),
			controlPlaneNamespace: "dapr-system",
			interval:              time.Second,
		}
	}

	anchors := map[string]string{"ca.crt": "anchors-pem"}

	t.Run("creates the configmap in namespaces with Dapr-enabled pods", func(t *testing.T) {
		s := newSync(sourceCM(anchors), daprPod("appns", "pod1"), daprPod("otherns", "pod2"))
		require.NoError(t, s.sync(t.Context()))

		for _, namespace := range []string{"appns", "otherns"} {
			cm, err := s.client.CoreV1().ConfigMaps(namespace).Get(t.Context(), "dapr-trust-bundle", metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, anchors, cm.Data)
		}
	})

	t.Run("updates a stale synced configmap", func(t *testing.T) {
		stale := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: "dapr-trust-bundle", Namespace: "appns"},
			Data:       map[string]string{"ca.crt": "old-anchors-pem"},
		}
		s := newSync(sourceCM(anchors), stale, daprPod("appns", "pod1"))
		require.NoError(t, s.sync(t.Context()))

		cm, err := s.client.CoreV1().ConfigMaps("appns").Get(t.Context(), "dapr-trust-bundle", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, anchors, cm.Data)
	})

	t.Run("ignores namespaces without Dapr-enabled pods", func(t *testing.T) {
		plainPod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "plain", Namespace: "plainns"}}
		s := newSync(sourceCM(anchors), plainPod)
		require.NoError(t, s.sync(t.Context()))

		_, err := s.client.CoreV1().ConfigMaps("plainns").Get(t.Context(), "dapr-trust-bundle", metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "no configmap may be created in namespaces without Dapr workloads")
	})

	t.Run("does not touch the control-plane namespace source", func(t *testing.T) {
		s := newSync(sourceCM(anchors), daprPod("dapr-system", "sentrypod"))
		require.NoError(t, s.sync(t.Context()))

		cm, err := s.client.CoreV1().ConfigMaps("dapr-system").Get(t.Context(), "dapr-trust-bundle", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, anchors, cm.Data)
	})

	t.Run("missing source configmap is a no-op", func(t *testing.T) {
		s := newSync(daprPod("appns", "pod1"))
		require.NoError(t, s.sync(t.Context()))

		_, err := s.client.CoreV1().ConfigMaps("appns").Get(t.Context(), "dapr-trust-bundle", metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err))
	})
}
