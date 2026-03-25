/*
Copyright 2024 The Dapr Authors
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

package controller

import (
	"context"
	"testing"

	"github.com/diagridio/go-etcd-cron/api"
	etcdcronfake "github.com/diagridio/go-etcd-cron/tests/framework/fake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	cronpkg "github.com/dapr/dapr/pkg/scheduler/server/internal/cron"
	cronfake "github.com/dapr/dapr/pkg/scheduler/server/internal/cron/fake"
)

// Test_SetCron_SwapAcrossRestarts simulates the app.go server restart loop.
// A single Controller is created once, then SetCron is called with a new cron
// on each "restart". The namespace reconciler must use the latest cron each
// time. This verifies that the reconciler doesn't hold onto a stale cron instance across restarts.
func Test_SetCron_SwapAcrossRestarts(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	nsReader := clientfake.NewClientBuilder().WithScheme(scheme).Build()
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}}

	c := new(Controller)
	nsctrl := &namespace{ctrl: c, nsReader: nsReader}

	// Before SetCron is called, reconcile should return an error.
	_, err := nsctrl.Reconcile(t.Context(), req)
	require.ErrorContains(t, err, "controller cron not yet initialized")

	// Simulate three server restarts, each with a different cron instance.
	// Each cron records which "generation" was called so we can verify the
	// reconciler always uses the latest one.
	for i := range 3 {
		gen := i
		var calledGen int
		fakeCron := cronfake.New().WithClient(func(context.Context) (api.Interface, error) {
			calledGen = gen
			return etcdcronfake.New(), nil
		})

		var cronIface cronpkg.Interface = fakeCron
		c.SetCron(cronIface)

		_, err := nsctrl.Reconcile(t.Context(), req)
		require.NoError(t, err)
		assert.Equal(t, gen, calledGen,
			"reconciler should use cron from restart generation %d", gen)
	}
}

func Test_Reconcile(t *testing.T) {
	tests := map[string]struct {
		ns                *corev1.Namespace
		cronClientError   error
		deletePError      error
		expErr            error
		expDeletePrefixes []string
	}{
		"etcd cron client returns error, should return error": {
			cronClientError: assert.AnError,
			ns:              &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}},
			expErr:          assert.AnError,
		},
		"namespace exists, do nothing": {
			ns: &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "test-ns"}},
		},
		"namespace doesn't exist, should delete prefixes": {
			expDeletePrefixes: []string{"actorreminder||test-ns||", "app||test-ns||"},
		},
		"error on deleting prefix should error": {
			deletePError:      assert.AnError,
			expDeletePrefixes: []string{"actorreminder||test-ns||", "app||test-ns||"},
			expErr:            assert.AnError,
		},
		"different namespace, should still delete prefixes": {
			ns:                &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "different-ns"}},
			expDeletePrefixes: []string{"actorreminder||test-ns||", "app||test-ns||"},
		},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-ns"}}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var prefixCalled []string
			cron := cronfake.New().WithClient(func(context.Context) (api.Interface, error) {
				etcdcron := etcdcronfake.New().WithDeletePrefixes(func(_ context.Context, prefixes ...string) error {
					prefixCalled = append(prefixCalled, prefixes...)
					return test.deletePError
				})
				return etcdcron, test.cronClientError
			})

			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))

			nsReaderBuilder := clientfake.NewClientBuilder().WithScheme(scheme)
			if test.ns != nil {
				nsReaderBuilder.WithObjects(test.ns)
			}

			c := new(Controller)
			var cronIface cronpkg.Interface = cron
			c.SetCron(cronIface)
			nsctrl := &namespace{
				ctrl:     c,
				nsReader: nsReaderBuilder.Build(),
			}

			result, err := nsctrl.Reconcile(t.Context(), req)
			assert.Equal(t, ctrl.Result{}, result)
			assert.Equal(t, test.expErr, err)
			assert.ElementsMatch(t, test.expDeletePrefixes, prefixCalled)
		})
	}
}
