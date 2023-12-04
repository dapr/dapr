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

package channels

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/middleware"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpendpapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	httpMiddleware "github.com/dapr/dapr/pkg/middleware/http"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
	"github.com/dapr/kit/logger"
)

func TestMiddlewareBuildPipeline(t *testing.T) {
	t.Run("build when no global config are set", func(t *testing.T) {
		ch := &Channels{}

		pipeline, err := ch.buildHTTPPipelineForSpec(&config.PipelineSpec{}, "test")
		require.NoError(t, err)
		assert.Empty(t, pipeline.Handlers)
	})

	t.Run("ignore component that does not exists", func(t *testing.T) {
		ch := &Channels{
			compStore: compstore.New(),
		}

		pipeline, err := ch.buildHTTPPipelineForSpec(&config.PipelineSpec{
			Handlers: []config.HandlerSpec{
				{
					Name:         "not_exists",
					Type:         "not_exists",
					Version:      "not_exists",
					SelectorSpec: config.SelectorSpec{},
				},
			},
		}, "test")
		require.NoError(t, err)
		assert.Empty(t, pipeline.Handlers)
	})

	compStore := compstore.New()
	require.NoError(t, compStore.AddPendingComponentForCommit(componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mymw1",
		},
		Spec: componentsapi.ComponentSpec{
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
	}))
	require.NoError(t, compStore.CommitPendingComponent())
	require.NoError(t, compStore.AddPendingComponentForCommit(componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mymw2",
		},
		Spec: componentsapi.ComponentSpec{
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
	}))
	require.NoError(t, compStore.CommitPendingComponent())

	t.Run("all components exists", func(t *testing.T) {
		ch := &Channels{
			compStore: compStore,
			meta:      meta.New(meta.Options{Mode: modes.StandaloneMode}),
			registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
				httpMiddlewareLoader.NewRegistry(),
			)).HTTPMiddlewares(),
		}
		called := 0
		ch.registry.RegisterComponent(
			func(_ logger.Logger) httpMiddlewareLoader.FactoryMethod {
				called++
				return func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
					return func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
					}, nil
				}
			},
			"fakemw",
		)

		pipeline, err := ch.buildHTTPPipelineForSpec(&config.PipelineSpec{
			Handlers: []config.HandlerSpec{
				{
					Name:    "mymw1",
					Type:    "middleware.http.fakemw",
					Version: "v1",
				},
				{
					Name:    "mymw2",
					Type:    "middleware.http.fakemw",
					Version: "v1",
				},
			},
		}, "test")
		require.NoError(t, err)
		assert.Len(t, pipeline.Handlers, 2)
		assert.Equal(t, 2, called)
	})

	testInitFail := func(ignoreErrors bool) func(t *testing.T) {
		compStore := compstore.New()
		require.NoError(t, compStore.AddPendingComponentForCommit(componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mymw",
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "middleware.http.fakemw",
				Version: "v1",
			},
		}))
		require.NoError(t, compStore.CommitPendingComponent())

		require.NoError(t, compStore.AddPendingComponentForCommit(componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "failmw",
			},
			Spec: componentsapi.ComponentSpec{
				Type:         "middleware.http.fakemw",
				Version:      "v1",
				IgnoreErrors: ignoreErrors,
				Metadata: []commonapi.NameValuePair{
					{Name: "fail", Value: commonapi.DynamicValue{JSON: v1.JSON{Raw: []byte("true")}}},
				},
			},
		}))
		require.NoError(t, compStore.CommitPendingComponent())
		return func(t *testing.T) {
			ch := &Channels{
				compStore: compStore,
				meta:      meta.New(meta.Options{}),
				registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
					httpMiddlewareLoader.NewRegistry(),
				)).HTTPMiddlewares(),
			}
			called := 0
			ch.registry.RegisterComponent(
				func(_ logger.Logger) httpMiddlewareLoader.FactoryMethod {
					called++
					return func(metadata middleware.Metadata) (httpMiddleware.Middleware, error) {
						if metadata.Properties["fail"] == "true" {
							return nil, errors.New("simulated failure")
						}

						return func(next http.Handler) http.Handler {
							return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})
						}, nil
					}
				},
				"fakemw",
			)

			pipeline, err := ch.buildHTTPPipelineForSpec(&config.PipelineSpec{
				Handlers: []config.HandlerSpec{
					{
						Name:    "mymw",
						Type:    "middleware.http.fakemw",
						Version: "v1",
					},
					{
						Name:    "failmw",
						Type:    "middleware.http.fakemw",
						Version: "v1",
					},
				},
			}, "test")

			assert.Equal(t, 2, called)

			if ignoreErrors {
				require.NoError(t, err)
				assert.Len(t, pipeline.Handlers, 1)
			} else {
				require.Error(t, err)
			}
		}
	}

	t.Run("one components fails to init", testInitFail(false))
	t.Run("one components fails to init but ignoreErrors is true", testInitFail(true))
}

func TestGetAppHTTPChannelConfigWithCustomChannel(t *testing.T) {
	ch := &Channels{
		compStore: compstore.New(),
		meta:      meta.New(meta.Options{Mode: modes.StandaloneMode}),
		appConnectionConfig: config.AppConnectionConfig{
			ChannelAddress: "my.app",
			Protocol:       "http",
			Port:           0,
		},
		registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
			httpMiddlewareLoader.NewRegistry(),
		)).HTTPMiddlewares(),
	}

	p, err := ch.BuildHTTPPipeline(&config.PipelineSpec{})
	require.NoError(t, err)

	c := ch.appHTTPChannelConfig(p)
	assert.Equal(t, "http://my.app:0", c.Endpoint)
}

func TestGetHTTPEndpointAppChannel(t *testing.T) {
	testPK, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	testPKBytes, err := x509.MarshalPKCS8PrivateKey(testPK)
	require.NoError(t, err)
	testPKPEM := pem.EncodeToMemory(&pem.Block{
		Type: "PRIVATE KEY", Bytes: testPKBytes,
	})

	cert := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),
	}

	testCertBytes, err := x509.CreateCertificate(rand.Reader, cert, cert, &testPK.PublicKey, testPK)
	require.NoError(t, err)

	testCertPEM := pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: testCertBytes,
	})

	t.Run("no TLS channel", func(t *testing.T) {
		ch := &Channels{
			compStore: compstore.New(),
			meta:      meta.New(meta.Options{Mode: modes.StandaloneMode}),
			appConnectionConfig: config.AppConnectionConfig{
				ChannelAddress: "my.app",
				Protocol:       "http",
				Port:           0,
			},
			registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
				httpMiddlewareLoader.NewRegistry(),
			)).HTTPMiddlewares(),
		}

		conf, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpendpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpendpapi.HTTPEndpointSpec{},
		})

		require.NoError(t, err)
		assert.Nil(t, conf.Client.Transport.(*http.Transport).TLSClientConfig)
	})

	t.Run("TLS channel with Root CA", func(t *testing.T) {
		ch := &Channels{
			compStore: compstore.New(),
			meta:      meta.New(meta.Options{Mode: modes.StandaloneMode}),
			appConnectionConfig: config.AppConnectionConfig{
				ChannelAddress: "my.app",
				Protocol:       "http",
				Port:           0,
			},
			registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
				httpMiddlewareLoader.NewRegistry(),
			)).HTTPMiddlewares(),
		}

		conf, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpendpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpendpapi.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					RootCA: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testCertPEM},
						},
					},
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testCertPEM},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testPKPEM},
						},
					},
				},
			},
		})

		require.NoError(t, err)
		assert.NotNil(t, conf.Client.Transport.(*http.Transport).TLSClientConfig)
	})

	t.Run("TLS channel without Root CA", func(t *testing.T) {
		ch := &Channels{
			compStore: compstore.New(),
			meta:      meta.New(meta.Options{Mode: modes.StandaloneMode}),
			appConnectionConfig: config.AppConnectionConfig{
				ChannelAddress: "my.app",
				Protocol:       "http",
				Port:           0,
			},
			registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
				httpMiddlewareLoader.NewRegistry(),
			)).HTTPMiddlewares(),
		}

		conf, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpendpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpendpapi.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testCertPEM},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testPKPEM},
						},
					},
				},
			},
		})

		require.NoError(t, err)
		assert.NotNil(t, conf.Client.Transport.(*http.Transport).TLSClientConfig)
	})

	t.Run("TLS channel with invalid Root CA", func(t *testing.T) {
		ch := &Channels{
			compStore: compstore.New(),
			meta:      meta.New(meta.Options{Mode: modes.StandaloneMode}),
			appConnectionConfig: config.AppConnectionConfig{
				ChannelAddress: "my.app",
				Protocol:       "http",
				Port:           0,
			},
			registry: registry.New(registry.NewOptions().WithHTTPMiddlewares(
				httpMiddlewareLoader.NewRegistry(),
			)).HTTPMiddlewares(),
		}

		_, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpendpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpendpapi.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					RootCA: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("asdsdsdassjkdctewzxabcdef")},
						},
					},
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testCertPEM},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: testPKPEM},
						},
					},
				},
			},
		})

		require.Error(t, err)
	})
}
