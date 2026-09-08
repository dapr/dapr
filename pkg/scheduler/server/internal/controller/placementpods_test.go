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

package controller

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeSink struct {
	observations []bool
}

func (f *fakeSink) SetKubernetesPresence(present bool) {
	f.observations = append(f.observations, present)
}

func placementPod(name, namespace string, labeled bool) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	if labeled {
		pod.Labels = map[string]string{"app": "dapr-placement-server"}
	}
	return pod
}

func Test_PlacementPodsReconcile(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		pods       []*corev1.Pod
		expPresent bool
	}{
		"no pods": {
			expPresent: false,
		},
		"labeled pod in the control plane namespace": {
			pods:       []*corev1.Pod{placementPod("dapr-placement-server-0", "dapr-system", true)},
			expPresent: true,
		},
		"pod without the placement label": {
			pods:       []*corev1.Pod{placementPod("other", "dapr-system", false)},
			expPresent: false,
		},
		"labeled pod in another namespace": {
			pods:       []*corev1.Pod{placementPod("dapr-placement-server-0", "other-ns", true)},
			expPresent: false,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			scheme := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(scheme))
			builder := clientfake.NewClientBuilder().WithScheme(scheme)
			for _, pod := range test.pods {
				builder = builder.WithObjects(pod)
			}

			c := new(Controller)
			sink := new(fakeSink)
			c.SetPresenceSink(sink)

			p := &placementPods{
				ctrl:      c,
				podReader: builder.Build(),
				namespace: "dapr-system",
			}

			_, err := p.Reconcile(t.Context(), ctrl.Request{
				NamespacedName: types.NamespacedName{Namespace: "dapr-system", Name: "dapr-placement-server-0"},
			})
			require.NoError(t, err)
			require.Len(t, sink.observations, 1)
			assert.Equal(t, test.expPresent, sink.observations[0])
		})
	}
}

// Test_SetPresenceSink_SwapAcrossRestarts simulates the app.go server
// restart loop: each incarnation's handoff is a new sink, and it must start
// from the current observation rather than wait for the next pod event.
func Test_SetPresenceSink_SwapAcrossRestarts(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	reader := clientfake.NewClientBuilder().WithScheme(scheme).
		WithObjects(placementPod("dapr-placement-server-0", "dapr-system", true)).
		Build()

	c := new(Controller)

	first := new(fakeSink)
	c.SetPresenceSink(first)
	assert.Empty(t, first.observations)

	p := &placementPods{ctrl: c, podReader: reader, namespace: "dapr-system"}
	_, err := p.Reconcile(t.Context(), ctrl.Request{
		NamespacedName: types.NamespacedName{Namespace: "dapr-system", Name: "dapr-placement-server-0"},
	})
	require.NoError(t, err)
	assert.Equal(t, []bool{true}, first.observations)

	second := new(fakeSink)
	c.SetPresenceSink(second)
	assert.Equal(t, []bool{true}, second.observations)
}
