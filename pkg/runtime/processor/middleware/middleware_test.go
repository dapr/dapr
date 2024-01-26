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

package middleware

import (
	"context"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribmiddleware "github.com/dapr/components-contrib/middleware"
	"github.com/dapr/dapr/pkg/apis/common"
	compapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	compmiddlehttp "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	daprmiddleware "github.com/dapr/dapr/pkg/middleware"
	"github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/logger"
)

func TestInit(t *testing.T) {
	t.Run("error when component type doesn't exist", func(t *testing.T) {
		m := New(Options{
			RegistryHTTP: registry.New(
				registry.NewOptions().WithHTTPMiddlewares(compmiddlehttp.NewRegistry()),
			).HTTPMiddlewares(),
			Meta: meta.New(meta.Options{}),
			HTTP: http.New(),
		})

		err := m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.uppercase",
				Version: "v1",
			},
		})
		require.Error(t, err)
		require.ErrorContains(t, err, "CREATE_COMPONENT_FAILURE")
	})

	t.Run("no error when http middleware component type exists", func(t *testing.T) {
		reg := registry.New(
			registry.NewOptions().WithHTTPMiddlewares(compmiddlehttp.NewRegistry()),
		).HTTPMiddlewares()
		m := New(Options{
			RegistryHTTP: reg,
			Meta:         meta.New(meta.Options{}),
			HTTP:         http.New(),
		})

		reg.RegisterComponent(func(logger.Logger) compmiddlehttp.FactoryMethod {
			return func(meta contribmiddleware.Metadata) (daprmiddleware.HTTP, error) {
				assert.Equal(t, "test", meta.Name)
				assert.Equal(t, map[string]string{"routes": `{"/foo":"/v1.0/invoke/nowhere/method/bar"}`}, meta.Properties)
				return nil, nil
			}
		}, "mock")

		err := m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.mock",
				Version: "v1",
				Metadata: []common.NameValuePair{{Name: "routes", Value: common.DynamicValue{
					JSON: apiextv1.JSON{Raw: []byte(`{"/foo":"/v1.0/invoke/nowhere/method/bar"}`)},
				}}},
			},
		})
		require.NoError(t, err)
	})

	t.Run("different version should error", func(t *testing.T) {
		reg := registry.New(
			registry.NewOptions().WithHTTPMiddlewares(compmiddlehttp.NewRegistry()),
		).HTTPMiddlewares()
		m := New(Options{
			RegistryHTTP: reg,
			Meta:         meta.New(meta.Options{}),
			HTTP:         http.New(),
		})

		reg.RegisterComponent(func(logger.Logger) compmiddlehttp.FactoryMethod {
			return func(meta contribmiddleware.Metadata) (daprmiddleware.HTTP, error) {
				return nil, nil
			}
		}, "mock")

		err := m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.mock",
				Version: "v2",
			},
		})
		require.Error(t, err)
	})

	t.Run("different type should error", func(t *testing.T) {
		reg := registry.New(
			registry.NewOptions().WithHTTPMiddlewares(compmiddlehttp.NewRegistry()),
		).HTTPMiddlewares()
		m := New(Options{
			RegistryHTTP: reg,
			Meta:         meta.New(meta.Options{}),
			HTTP:         http.New(),
		})

		reg.RegisterComponent(func(logger.Logger) compmiddlehttp.FactoryMethod {
			return func(meta contribmiddleware.Metadata) (daprmiddleware.HTTP, error) {
				return nil, nil
			}
		}, "notmock")

		err := m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.mock",
				Version: "v1",
			},
		})
		require.Error(t, err)
	})

	t.Run("middleware should be added to HTTP middleware manager", func(t *testing.T) {
		reg := registry.New(
			registry.NewOptions().WithHTTPMiddlewares(compmiddlehttp.NewRegistry()),
		).HTTPMiddlewares()
		mngr := http.New()

		pipeline := mngr.BuildPipelineFromSpec("test", &config.PipelineSpec{
			Handlers: []config.HandlerSpec{
				{
					Name:    "test",
					Type:    "middleware.http.mock",
					Version: "v1",
				},
			},
		})

		m := New(Options{
			RegistryHTTP: reg,
			Meta:         meta.New(meta.Options{}),
			HTTP:         mngr,
		})

		var rootCalled int
		handler := pipeline(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
			rootCalled++
		}))

		var middlewareCalled int
		reg.RegisterComponent(func(logger.Logger) compmiddlehttp.FactoryMethod {
			return func(meta contribmiddleware.Metadata) (daprmiddleware.HTTP, error) {
				return func(next nethttp.Handler) nethttp.Handler {
					middlewareCalled++
					return next
				}, nil
			}
		}, "mock")

		assert.Equal(t, 0, rootCalled)
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, rootCalled)
		assert.Equal(t, 0, middlewareCalled)

		err := m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: compapi.ComponentSpec{
				Type:    "middleware.http.mock",
				Version: "v1",
			},
		})
		require.NoError(t, err)

		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 2, rootCalled)
		assert.Equal(t, 1, middlewareCalled)
	})

	t.Run("middleware should be removed from HTTP middleware manager when closed", func(t *testing.T) {
		reg := registry.New(
			registry.NewOptions().WithHTTPMiddlewares(compmiddlehttp.NewRegistry()),
		).HTTPMiddlewares()
		mngr := http.New()

		pipeline := mngr.BuildPipelineFromSpec("test", &config.PipelineSpec{
			Handlers: []config.HandlerSpec{
				{Name: "test1", Type: "middleware.http.mock", Version: "v1"},
				{Name: "test2", Type: "middleware.http.mock", Version: "v1"},
			},
		})

		m := New(Options{
			RegistryHTTP: reg,
			Meta:         meta.New(meta.Options{}),
			HTTP:         mngr,
		})

		var rootCalled int
		handler := pipeline(nethttp.HandlerFunc(func(nethttp.ResponseWriter, *nethttp.Request) {
			rootCalled++
		}))

		var middlewareCalled int
		reg.RegisterComponent(func(logger.Logger) compmiddlehttp.FactoryMethod {
			return func(meta contribmiddleware.Metadata) (daprmiddleware.HTTP, error) {
				return func(next nethttp.Handler) nethttp.Handler {
					return nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
						middlewareCalled++
						next.ServeHTTP(w, r)
					})
				}, nil
			}
		}, "mock")

		require.NoError(t, m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "test1"},
			Spec:       compapi.ComponentSpec{Type: "middleware.http.mock", Version: "v1"},
		}))
		require.NoError(t, m.Init(context.Background(), compapi.Component{
			ObjectMeta: metav1.ObjectMeta{Name: "test2"},
			Spec:       compapi.ComponentSpec{Type: "middleware.http.mock", Version: "v1"},
		}))

		assert.Equal(t, 0, rootCalled)
		assert.Equal(t, 0, middlewareCalled)
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 1, rootCalled)
		assert.Equal(t, 2, middlewareCalled)
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 2, rootCalled)
		assert.Equal(t, 4, middlewareCalled)

		m.Close(compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "test1"}})
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 3, rootCalled)
		assert.Equal(t, 5, middlewareCalled)

		m.Close(compapi.Component{ObjectMeta: metav1.ObjectMeta{Name: "test2"}})
		handler.ServeHTTP(nil, nil)
		assert.Equal(t, 4, rootCalled)
		assert.Equal(t, 5, middlewareCalled)
	})
}
