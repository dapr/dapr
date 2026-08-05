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

package cache

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kcache "k8s.io/client-go/tools/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/dapr/dapr/pkg/injector/annotations"
)

func Test_podMetadataTransform(t *testing.T) {
	t.Run("dapr pod keeps only dapr annotations and drops labels", func(t *testing.T) {
		in := PodMetadataPodGVKObject()
		in.Name = "myapp"
		in.Namespace = "default"
		in.Labels = map[string]string{"app": "myapp", "dapr.io/sidecar-injected": "true"}
		in.Annotations = map[string]string{
			annotations.KeyEnabled:                             "true",
			annotations.KeyAppID:                               "myapp",
			annotations.KeyConfig:                              "myconfig",
			"kubectl.kubernetes.io/last-applied-configuration": "a very large blob",
		}

		obj, err := podMetadataTransform(in)
		require.NoError(t, err)
		got := obj.(*metav1.PartialObjectMetadata)

		require.Equal(t, "myapp", got.Name)
		require.Equal(t, "default", got.Namespace)
		require.Nil(t, got.Labels)
		require.Equal(t, map[string]string{
			annotations.KeyEnabled: "true",
			annotations.KeyAppID:   "myapp",
			annotations.KeyConfig:  "myconfig",
		}, got.Annotations)
	})

	t.Run("non-dapr pods are coalesced into the sinkhole", func(t *testing.T) {
		store := kcache.NewStore(func(obj any) (string, error) {
			o := obj.(client.Object)
			return o.GetNamespace() + "/" + o.GetName(), nil
		})

		for _, nn := range [][2]string{{"pod1", "ns1"}, {"pod2", "ns2"}, {"pod3", "ns3"}} {
			in := PodMetadataPodGVKObject()
			in.Name, in.Namespace = nn[0], nn[1]
			obj, err := podMetadataTransform(in)
			require.NoError(t, err)
			require.NoError(t, store.Add(obj))
		}

		require.Len(t, store.List(), 1)
	})
}
