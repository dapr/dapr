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
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	securityfake "github.com/dapr/dapr/pkg/security/fake"
)

const testAnchors = "-----BEGIN CERTIFICATE-----\ntest\n-----END CERTIFICATE-----\n"

func testReconciler(t *testing.T, cl client.Client) *Reconciler {
	t.Helper()
	sec := securityfake.New().WithCurrentTrustAnchorsFn(func(context.Context) ([]byte, error) {
		return []byte(testAnchors), nil
	})
	return New(Options{
		Client:   cl,
		Reader:   cl,
		Security: sec,
	})
}

func namespace(name string) *corev1.Namespace {
	return &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func TestReconcile(t *testing.T) {
	t.Parallel()

	req := ctrl.Request{NamespacedName: client.ObjectKey{Name: "frodo"}}

	t.Run("creates the ConfigMap when missing", func(t *testing.T) {
		t.Parallel()
		cl := fake.NewClientBuilder().WithObjects(namespace("frodo")).Build()
		r := testReconciler(t, cl)

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)

		var cm corev1.ConfigMap
		require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Namespace: "frodo", Name: "dapr-root-ca.crt"}, &cm))
		assert.Equal(t, map[string]string{"ca.crt": testAnchors}, cm.Data)
		assert.Equal(t, "dapr-operator", cm.Labels["app.kubernetes.io/managed-by"])
	})

	t.Run("updates the ConfigMap on drift", func(t *testing.T) {
		t.Parallel()
		cl := fake.NewClientBuilder().WithObjects(namespace("frodo"), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "frodo", Name: "dapr-root-ca.crt"},
			Data:       map[string]string{"ca.crt": "stale"},
		}).Build()
		r := testReconciler(t, cl)

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)

		var cm corev1.ConfigMap
		require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Namespace: "frodo", Name: "dapr-root-ca.crt"}, &cm))
		assert.Equal(t, map[string]string{"ca.crt": testAnchors}, cm.Data)
	})

	t.Run("restores foreign edits including extra keys", func(t *testing.T) {
		t.Parallel()
		cl := fake.NewClientBuilder().WithObjects(namespace("frodo"), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "frodo", Name: "dapr-root-ca.crt"},
			Data:       map[string]string{"ca.crt": testAnchors, "rogue": "key"},
		}).Build()
		r := testReconciler(t, cl)

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)

		var cm corev1.ConfigMap
		require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Namespace: "frodo", Name: "dapr-root-ca.crt"}, &cm))
		assert.Equal(t, map[string]string{"ca.crt": testAnchors}, cm.Data)
	})

	t.Run("does not write when already up to date", func(t *testing.T) {
		t.Parallel()
		cl := fake.NewClientBuilder().WithObjects(namespace("frodo"), &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: "frodo", Name: "dapr-root-ca.crt"},
			Data:       map[string]string{"ca.crt": testAnchors},
		}).WithInterceptorFuncs(interceptor.Funcs{
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
				return errors.New("update should not be called")
			},
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
				return errors.New("create should not be called")
			},
		}).Build()
		r := testReconciler(t, cl)

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)
	})

	t.Run("no-op for a missing namespace", func(t *testing.T) {
		t.Parallel()
		cl := fake.NewClientBuilder().Build()
		r := testReconciler(t, cl)

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)

		var cm corev1.ConfigMap
		err = cl.Get(t.Context(), client.ObjectKey{Namespace: "frodo", Name: "dapr-root-ca.crt"}, &cm)
		assert.Error(t, err)
	})

	t.Run("no-op for a terminating namespace", func(t *testing.T) {
		t.Parallel()
		ns := namespace("frodo")
		ns.Finalizers = []string{"kubernetes"}
		cl := fake.NewClientBuilder().WithObjects(ns).Build()
		require.NoError(t, cl.Delete(t.Context(), ns))
		r := testReconciler(t, cl)

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)

		var cm corev1.ConfigMap
		err = cl.Get(t.Context(), client.ObjectKey{Namespace: "frodo", Name: "dapr-root-ca.crt"}, &cm)
		assert.Error(t, err)
	})

	t.Run("custom ConfigMap name is respected", func(t *testing.T) {
		t.Parallel()
		cl := fake.NewClientBuilder().WithObjects(namespace("frodo")).Build()
		sec := securityfake.New().WithCurrentTrustAnchorsFn(func(context.Context) ([]byte, error) {
			return []byte(testAnchors), nil
		})
		r := New(Options{Client: cl, Reader: cl, Security: sec, ConfigMapName: "my-trust-bundle"})

		_, err := r.Reconcile(t.Context(), req)
		require.NoError(t, err)

		var cm corev1.ConfigMap
		require.NoError(t, cl.Get(t.Context(), client.ObjectKey{Namespace: "frodo", Name: "my-trust-bundle"}, &cm))
		assert.Equal(t, map[string]string{"ca.crt": testAnchors}, cm.Data)
	})
}
