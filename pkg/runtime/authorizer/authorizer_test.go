/*
Copyright 2021 The Dapr Authors
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

package authorizer

import (
	"testing"

	"github.com/stretchr/testify/assert"

	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	daprt "github.com/dapr/dapr/pkg/testing"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAuthorizedComponents(t *testing.T) {
	testCompName := "fakeComponent"

	t.Run("standalone mode, no namespce", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "test",
		})
		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
		assert.Equal(t, testCompName, components[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("namespace match", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{daprt.TestRuntimeConfigID}

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{"other"}

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{daprt.TestRuntimeConfigID}

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{"other"}

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("no authorizers", func(t *testing.T) {
		auth := New(Options{
			ID: "test",
			// Namespace mismatch, should be accepted anyways
			Namespace: "a",
		})
		auth.componentAuthorizers = []ComponentAuthorizer{}

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 1, len(components))
		assert.Equal(t, testCompName, components[0].Name)
	})

	t.Run("only deny all", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})
		auth.componentAuthorizers = []ComponentAuthorizer{
			func(component compapi.Component) bool {
				return false
			},
		}

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})

	t.Run("additional authorizer denies all", func(t *testing.T) {
		auth := New(Options{
			ID:        daprt.TestRuntimeConfigID,
			Namespace: "a",
		})
		auth.componentAuthorizers = append(auth.componentAuthorizers, func(component compapi.Component) bool {
			return false
		})

		component := compapi.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := auth.GetAuthorizedObjects([]compapi.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]compapi.Component)
		assert.True(t, ok)
		assert.Equal(t, 0, len(components))
	})
}

func TestAuthorizedHTTPEndpoints(t *testing.T) {
	auth := New(Options{
		ID:        daprt.TestRuntimeConfigID,
		Namespace: "a",
	})
	endpoint := httpendapi.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testEndpoint",
		},
		Spec: httpendapi.HTTPEndpointSpec{
			BaseURL: "http://api.test.com",
		},
	}

	t.Run("standalone mode, no namespace", func(t *testing.T) {
		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
		assert.Equal(t, endpoint.Name, endpoints[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("namespace match", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"
		endpoint.Scopes = []string{daprt.TestRuntimeConfigID}

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"
		endpoint.Scopes = []string{"other"}

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"
		endpoint.Scopes = []string{daprt.TestRuntimeConfigID}

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"
		endpoint.Scopes = []string{"other"}

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 0, len(endpoints))
	})

	t.Run("no authorizers", func(t *testing.T) {
		auth.httpEndpointAuthorizers = []HTTPEndpointAuthorizer{}
		// Namespace mismatch, should be accepted anyways
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"

		endpointObjs := auth.GetAuthorizedObjects([]httpendapi.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpendapi.HTTPEndpoint)
		assert.True(t, ok)
		assert.Equal(t, 1, len(endpoints))
		assert.Equal(t, endpoint.Name, endpoints[0].ObjectMeta.Name)
	})
}
