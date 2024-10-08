/*
Copyright 2022 The Dapr Authors
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

package loadtest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/dapr/dapr/pkg/client/clientset/versioned/scheme"

	v1 "github.com/grafana/k6-operator/api/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/rest/fake"
)

func TestK6Client(t *testing.T) {
	const (
		fakeNamespace = "fake-namespace"
		k6Name        = "k6-test"
	)
	k6, err := newK6Client(&rest.Config{}, fakeNamespace)
	require.NoError(t, err)
	getClient := func(onRequest func(r *http.Request)) *fake.RESTClient {
		return &fake.RESTClient{
			Client: fake.CreateHTTPClient(func(r *http.Request) (*http.Response, error) {
				onRequest(r)
				return &http.Response{
					Body:       io.NopCloser(bytes.NewBufferString("{}")),
					StatusCode: http.StatusOK,
				}, nil
			}),
			GroupVersion:         v1.GroupVersion,
			VersionedAPIPath:     "/apis",
			NegotiatedSerializer: serializer.NewCodecFactory(scheme.Scheme),
		}
	}

	t.Run("Delete should call rest DELETE", func(t *testing.T) {
		called := 0
		k6.client = getClient(func(r *http.Request) {
			called++
			assert.Equal(t, "DELETE", r.Method)
		})
		require.NoError(t, k6.Delete(context.Background(), k6Name, metav1.DeleteOptions{}))
		assert.Equal(t, 1, called)
	})
	t.Run("Get should call rest GET", func(t *testing.T) {
		called := 0
		k6.client = getClient(func(r *http.Request) {
			called++
			assert.Equal(t, "GET", r.Method)
		})
		_, err = k6.Get(context.Background(), k6Name)
		require.NoError(t, err)
		assert.Equal(t, 1, called)
	})
	t.Run("Create should call rest POST", func(t *testing.T) {
		called := 0
		k6.client = getClient(func(r *http.Request) {
			called++
			assert.Equal(t, "POST", r.Method)
		})
		_, err = k6.Create(context.Background(), &v1.K6{})
		require.NoError(t, err)
		assert.Equal(t, 1, called)
	})
	t.Run("List should call rest GET with filters", func(t *testing.T) {
		called := 0
		k6.client = getClient(func(r *http.Request) {
			called++
			assert.Equal(t, "GET", r.Method)
		})
		_, err = k6.List(context.Background(), metav1.ListOptions{})
		require.NoError(t, err)
		assert.Equal(t, 1, called)
	})
}
