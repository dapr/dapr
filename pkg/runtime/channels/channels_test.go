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
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/middleware"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsapi "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	httpenpapi "github.com/dapr/dapr/pkg/apis/httpEndpoint/v1alpha1"
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
		assert.Len(t, pipeline.Handlers, 0)
	})

	compStore := compstore.New()
	compStore.AddComponent(componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mymw1",
		},
		Spec: componentsapi.ComponentSpec{
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
	})
	compStore.AddComponent(componentsapi.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mymw2",
		},
		Spec: componentsapi.ComponentSpec{
			Type:    "middleware.http.fakemw",
			Version: "v1",
		},
	})

	t.Run("all components exists", func(t *testing.T) {
		ch := &Channels{
			compStore: compStore,
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
		compStore.AddComponent(componentsapi.Component{
			ObjectMeta: metav1.ObjectMeta{
				Name: "mymw",
			},
			Spec: componentsapi.ComponentSpec{
				Type:    "middleware.http.fakemw",
				Version: "v1",
			},
		})

		compStore.AddComponent(componentsapi.Component{
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
		})
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
	assert.Nil(t, err)

	c := ch.appHTTPChannelConfig(p)
	assert.Equal(t, "http://my.app:0", c.Endpoint)
}

func TestGetHTTPEndpointAppChannel(t *testing.T) {
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

		conf, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpenpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpenpapi.HTTPEndpointSpec{},
		})

		assert.NoError(t, err)
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

		conf, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpenpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpenpapi.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					RootCA: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n")},
						},
					},
				},
			},
		})

		assert.NoError(t, err)
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

		conf, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpenpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpenpapi.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n")},
						},
					},
				},
			},
		})

		assert.NoError(t, err)
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

		_, err := ch.getHTTPEndpointAppChannel(httpMiddleware.Pipeline{}, httpenpapi.HTTPEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name: "test",
			},
			Spec: httpenpapi.HTTPEndpointSpec{
				ClientTLS: &commonapi.TLS{
					RootCA: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("asdsdsdassjkdctewzxabcdef")},
						},
					},
					Certificate: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN CERTIFICATE-----\nMIID8zCCAtugAwIBAgIUSj2XqIQo/xmVVuw1+nmIcdmr48IwDQYJKoZIhvcNAQEL\nBQAwfjELMAkGA1UEBhMCVVMxEzARBgNVBAgMCkNhbGlmb3JuaWExFjAUBgNVBAcM\nDU1vdW50YWluIFZpZXcxGjAYBgNVBAoMEVlvdXIgT3JnYW5pemF0aW9uMRIwEAYD\nVQQLDAlZb3VyIFVuaXQxEjAQBgNVBAMMCWxvY2FsaG9zdDAeFw0yMzA3MTQyMTQ1\nNDRaFw0zMzA3MTEyMTQ1NDRaMH4xCzAJBgNVBAYTAlVTMRMwEQYDVQQIDApDYWxp\nZm9ybmlhMRYwFAYDVQQHDA1Nb3VudGFpbiBWaWV3MRowGAYDVQQKDBFZb3VyIE9y\nZ2FuaXphdGlvbjESMBAGA1UECwwJWW91ciBVbml0MRIwEAYDVQQDDAlsb2NhbGhv\nc3QwggEiMA0GCSqGSIb3DQEBAQUAA4IBDwAwggEKAoIBAQCmWsrT0w4s0g7Ld1g/\nIa0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6MJenJ\n0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyycKoG\nNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFLfOD6\nZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdAKe65\nSEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiVpvKE\nLzyBAgMBAAGjaTBnMB0GA1UdDgQWBBQj02E1/fJHbm8GnOMNxfSIRI2cqjAfBgNV\nHSMEGDAWgBQj02E1/fJHbm8GnOMNxfSIRI2cqjAPBgNVHRMBAf8EBTADAQH/MBQG\nA1UdEQQNMAuCCWxvY2FsaG9zdDANBgkqhkiG9w0BAQsFAAOCAQEANRqDGWWdaXwg\nhkOz/Y9gkil6WYQ4d7muAAqiEZBqg+yAisGrv+MPeyjsrq1WPbIgcdlSjArG7zGV\n/5SPZwsuTeivdFs0DHbcvoKA8hqN2n4OmtjzdommBWA2618fzByOufECWRmUBxH3\nqmkmQ+aj4wKXm2U22OFxmWPTJbtwi8MDppl+G3AH4VpRi6qhSmLBSAO3AEBr7UJN\nIRHeUzBsFDLE++a3WYRnI0kA9og9F+zbnD+kNMxxTfmZqB6T9iFHIzkxZw7RELey\nz2PxQioHRQtp0rVZWnDt6+qSfrqftZgszrM+06n1MHTPBMusBfG/uyEAc4EAStCC\njXHuph+Ctg==\n-----END CERTIFICATE-----\n")},
						},
					},
					PrivateKey: &commonapi.TLSDocument{
						Value: &commonapi.DynamicValue{
							JSON: v1.JSON{Raw: []byte("-----BEGIN PRIVATE KEY-----\nMIIEvAIBADANBgkqhkiG9w0BAQEFAASCBKYwggSiAgEAAoIBAQCmWsrT0w4s0g7L\nd1g/Ia0qM2v6tXG8L/150kwM7V/wRSVCx+Tmv+aAMAJzJtXwMAj9jHt+sH5vOm6M\nJenJ0sTyUcCC8FhLa0VWxbajZOn8wKaUuZzLj3Uye/q4z0l6rtu35pjZCjDKphyy\ncKoGNG2MwcGNNBUZP0W6Idw0PZaNstnXnTHIWE6axOA9bzB5iKtLTVkQDkuEJWFL\nfOD6ZN4ISA+7HvbMgjREeIuNJqNI/746Egza5a4ZPh3KvyIPEdH2SdDy+9obCsdA\nKe65SEIPy1n4zRy3/7K1bVTu18JoWsNtE+5GDWVgFU3AVZ9ic6Jt0N0WNaRuUiiV\npvKELzyBAgMBAAECggEABXHZS5+PyjXB2DT6xW4zvbrbIOSJaXBkqnUQmie2ySVq\nN8pVGpxTTgTEP8KYo/jegnXzoMzkBn3yGlIvWbS1T30PgPme2jETnuhvtt9ZrTUc\n/qcok50JZ/KY3S2jqQlKFbXNcOUdfbR8IfcACZ3zq/S3ggifXCku/g2XqHoPkGmp\nokEjXEJLX9f/ABgik3a5aaSCMYfCU9PzDNCM7vjiWxUvO0V5kjqYae+SpMQCvvTb\n1JEMEsawCEeGI9BBnj4o1x7xGDpFn4Yt6MznaHivANMHqqNKaH3LK6rFZGdnj7d6\nutpJ22QZUBYhZ1+Hz+WNUQD+z+O2NGfMEo0PZvb1MQKBgQDZ6TlehduU0JcgKUI+\nFSq5wAk+Eil158arCU3CWnn69VLUu5lAjkU4dXjmkg/c9nxRpg5kuWr8FGMmzTpx\nWlWZj1b72iTQuWX0fphNif3mljeDNLl5z0fGegHjH+KkGb9y6t06oKrsEZbxn4y0\nzOLfl1t85tPAMP6RuvfawpjBjQKBgQDDbpifamPx/GE1JdKj6M6At42kmDczd5P0\nlgN39BRC6fUS19CPurKVWF501+w52rxg1NWhtQVmW1DUdWpDUwQtRPXo43Ku8MPN\nRMD8Uj+PNgXgWnWdcfGbniB8xHqsE8N7MgKU5IZQOqEIDCVE4RGheTYGNgDZ16Rz\nILmZ14E3xQKBgHtI3fJCXSbmlHnXneit5QxOP2xkrhxM0zN1Ag9RTO3U2dYNhPjn\nBPaaT5pzTJJAybkP79jApmyTxDzxo3z6FK/aTuYSVv3Xxnz7GoPT7FgG6MVMkRr/\nUKZT5LlxErKw9oW3pw5CVDFXCkUNdXfc6waBBXu2xFpZ3czpMM0Nh4sJAoGAEpGo\nmMUQGAcF6XndiMtvC5XlNHVuEUrUWRID5FrhrfXy3kZ5P57apwwNdYaqoFijO4Qd\nhE7h43bbuEQrw5fYtsBtqSIrXGnuAMv+ljruZRoZ9tZBhKM19LZSmehFS6JZGZSH\n4EPSaz8W29/jjqbf+Pq+YlqxPAGcU4ARgoeSdI0CgYACG9WZMbri9Eas1qA3vP86\nVW917u7CKt3O6Gsr4F5BNIH3Qx9BReLB9cDvhyko/JAln0MiNq2nExeuFlhEqVsx\nmn681Xm6yPht1PNeTkrRroXJIVzbBOldFW7evX/g/izeiXH6YhfGKD6B8dBfUftx\nNcs6FFLydFcIdIxebYjYnQ==\n-----END PRIVATE KEY-----\n")},
						},
					},
				},
			},
		})

		assert.Error(t, err)
	})
}
