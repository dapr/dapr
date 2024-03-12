/*
Copyright 2023 The Dapr Authors
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpEndpointV1alpha1 "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	"github.com/dapr/dapr/pkg/config"
	daprt "github.com/dapr/dapr/pkg/testing"
)

func TestAuthorizedComponents(t *testing.T) {
	testCompName := "fakeComponent"

	t.Run("standalone mode, no namespce", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "default",
			GlobalConfig: &config.Configuration{},
		})
		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Len(t, components, 1)
		assert.Equal(t, testCompName, components[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Empty(t, components)
	})

	t.Run("namespace match", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Len(t, components, 1)
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{daprt.TestRuntimeConfigID}

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Len(t, components, 1)
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{"other"}

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Empty(t, components)
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{daprt.TestRuntimeConfigID}

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Empty(t, components)
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{"other"}

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Empty(t, components)
	})

	t.Run("no authorizers", func(t *testing.T) {
		auth := New(Options{
			ID: "test",
			// Namespace mismatch, should be accepted anyways
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})
		auth.componentAuthorizers = []ComponentAuthorizer{}

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName
		component.ObjectMeta.Namespace = "b"

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Len(t, components, 1)
		assert.Equal(t, testCompName, components[0].Name)
	})

	t.Run("only deny all", func(t *testing.T) {
		auth := New(Options{
			ID:           daprt.TestRuntimeConfigID,
			Namespace:    "a",
			GlobalConfig: &config.Configuration{},
		})
		auth.componentAuthorizers = []ComponentAuthorizer{
			func(component componentsV1alpha1.Component) bool {
				return false
			},
		}

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Empty(t, components)
	})

	t.Run("additional authorizer denies all", func(t *testing.T) {
		auth := New(Options{
			ID:        "test",
			Namespace: "a",
			GlobalConfig: &config.Configuration{
				Spec: config.ConfigurationSpec{},
			},
		})

		auth.componentAuthorizers = append(auth.componentAuthorizers, func(component componentsV1alpha1.Component) bool {
			return false
		})

		component := componentsV1alpha1.Component{}
		component.ObjectMeta.Name = testCompName

		componentObj := auth.GetAuthorizedObjects([]componentsV1alpha1.Component{component}, auth.IsObjectAuthorized)
		components, ok := componentObj.([]componentsV1alpha1.Component)
		assert.True(t, ok)
		assert.Empty(t, components)
	})
}

func TestAuthorizedHTTPEndpoints(t *testing.T) {
	auth := New(Options{
		ID:        daprt.TestRuntimeConfigID,
		Namespace: "a",
		GlobalConfig: &config.Configuration{
			Spec: config.ConfigurationSpec{},
		},
	})
	endpoint := httpEndpointV1alpha1.HTTPEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: "testEndpoint",
		},
		Spec: httpEndpointV1alpha1.HTTPEndpointSpec{
			BaseURL: "http://api.test.com",
		},
	}

	t.Run("standalone mode, no namespace", func(t *testing.T) {
		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Len(t, endpoints, 1)
		assert.Equal(t, endpoint.Name, endpoints[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Empty(t, endpoints)
	})

	t.Run("namespace match", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Len(t, endpoints, 1)
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"
		endpoint.Scopes = []string{daprt.TestRuntimeConfigID}

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Len(t, endpoints, 1)
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "a"
		endpoint.Scopes = []string{"other"}

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Empty(t, endpoints)
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"
		endpoint.Scopes = []string{daprt.TestRuntimeConfigID}

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Empty(t, endpoints)
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"
		endpoint.Scopes = []string{"other"}

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Empty(t, endpoints)
	})

	t.Run("no authorizers", func(t *testing.T) {
		auth.httpEndpointAuthorizers = []HTTPEndpointAuthorizer{}
		// Namespace mismatch, should be accepted anyways
		auth.namespace = "a"
		endpoint.ObjectMeta.Namespace = "b"

		endpointObjs := auth.GetAuthorizedObjects([]httpEndpointV1alpha1.HTTPEndpoint{endpoint}, auth.IsObjectAuthorized)
		endpoints, ok := endpointObjs.([]httpEndpointV1alpha1.HTTPEndpoint)
		assert.True(t, ok)
		assert.Len(t, endpoints, 1)
		assert.Equal(t, endpoint.Name, endpoints[0].ObjectMeta.Name)
	})
}
