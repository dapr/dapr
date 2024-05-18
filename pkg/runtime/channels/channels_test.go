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
	"math/big"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	commonapi "github.com/dapr/dapr/pkg/apis/common"
	httpendpapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
	httpMiddlewareLoader "github.com/dapr/dapr/pkg/components/middleware/http"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/registry"
)

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

		conf, err := ch.getHTTPEndpointAppChannel(httpendpapi.HTTPEndpoint{
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

		conf, err := ch.getHTTPEndpointAppChannel(httpendpapi.HTTPEndpoint{
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

		conf, err := ch.getHTTPEndpointAppChannel(httpendpapi.HTTPEndpoint{
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

		_, err := ch.getHTTPEndpointAppChannel(httpendpapi.HTTPEndpoint{
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
