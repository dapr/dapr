// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	"github.com/dapr/dapr/pkg/channel"
	http_channel "github.com/dapr/dapr/pkg/channel/http"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/config"
	tracing "github.com/dapr/dapr/pkg/diagnostics"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/dapr/pkg/sentry/certs"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TestRuntimeConfigID = "consumer0"
)

var testCertRoot = `-----BEGIN CERTIFICATE-----
MIIBjjCCATOgAwIBAgIQdZeGNuAHZhXSmb37Pnx2QzAKBggqhkjOPQQDAjAYMRYw
FAYDVQQDEw1jbHVzdGVyLmxvY2FsMB4XDTIwMDIwMTAwMzUzNFoXDTMwMDEyOTAw
MzUzNFowGDEWMBQGA1UEAxMNY2x1c3Rlci5sb2NhbDBZMBMGByqGSM49AgEGCCqG
SM49AwEHA0IABAeMFRst4JhcFpebfgEs1MvJdD7h5QkCbLwChRHVEUoaDqd1aYjm
bX5SuNBXz5TBEhHfTV3Objh6LQ2N+CBoCeOjXzBdMA4GA1UdDwEB/wQEAwIBBjAS
BgNVHRMBAf8ECDAGAQH/AgEBMB0GA1UdDgQWBBRBWthv5ZQ3vALl2zXWwAXSmZ+m
qTAYBgNVHREEETAPgg1jbHVzdGVyLmxvY2FsMAoGCCqGSM49BAMCA0kAMEYCIQDN
rQNOck4ENOhmLROE/wqH0MKGjE6P8yzesgnp9fQI3AIhAJaVPrZloxl1dWCgmNWo
Iklq0JnMgJU7nS+VpVvlgBN8
-----END CERTIFICATE-----`

type MockKubernetesStateStore struct {
}

func (m *MockKubernetesStateStore) Init(metadata secretstores.Metadata) error {
	return nil
}

func (m *MockKubernetesStateStore) GetSecret(req secretstores.GetSecretRequest) (secretstores.GetSecretResponse, error) {
	return secretstores.GetSecretResponse{
		Data: map[string]string{
			"key1":   "value1",
			"_value": "_value_data",
			"name1":  "value1",
		},
	}, nil
}

func NewMockKubernetesStore() secretstores.SecretStore {
	return &MockKubernetesStateStore{}
}

func TestNewRuntime(t *testing.T) {
	// act
	r := NewDaprRuntime(&Config{}, &config.Configuration{})

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	initMockPubSubForRuntime := func(rt *DaprRuntime) *daprt.MockPubSub {
		mockPubSub := new(daprt.MockPubSub)
		rt.pubSubRegistry.Register(
			pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
				return mockPubSub
			}),
		)

		expectedMetadata := pubsub.Metadata{
			Properties: getFakeProperties(),
		}

		mockPubSub.On("Init", expectedMetadata).Return(nil)
		mockPubSub.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("func(*pubsub.NewMessage) error")).Return(nil)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		return mockPubSub
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 2 topics via http app channel
		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\", \"topic1\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "404"},
			Data:     nil,
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("publish adapter is nil, no pub sub component", func(t *testing.T) {
		rt = NewTestDaprRuntime(modes.StandaloneMode)
		a := rt.getPublishAdapter()
		assert.Nil(t, a)
	})

	t.Run("publish adapter not nil, with pub sub component", func(t *testing.T) {
		rt = NewTestDaprRuntime(modes.StandaloneMode)
		rt.pubSub = initMockPubSubForRuntime(rt)
		a := rt.getPublishAdapter()
		assert.NotNil(t, a)
	})

	t.Run("test subscribe, app allowed 1 topic", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app allowed 2 topic", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\", \"topic1\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
	})

	t.Run("test subscribe, app not allowed 1 topic", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic3\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
	})

	t.Run("test subscribe, app not allowed 1 topic, allowed one topic", func(t *testing.T) {
		mockPubSub := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\", \"topic3\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()
		assert.Nil(t, err)

		rt.pubSub = &mockPublishPubSub{}
		err = rt.Publish(&pubsub.PublishRequest{
			Topic: "topic0",
		})
		assert.Nil(t, err)
	})

	t.Run("test publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "dapr/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHTTPResponse, nil)

		// act
		err := rt.initPubSub()
		assert.Nil(t, err)

		rt.pubSub = &mockPublishPubSub{}
		err = rt.Publish(&pubsub.PublishRequest{
			Topic: "topic5",
		})
		assert.NotNil(t, err)
	})

	t.Run("test allowed topics, no scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = []string{"topic1"}
		a := rt.isPubSubOperationAllowed("topic1", rt.scopedPublishings)
		assert.True(t, a)
	})

	t.Run("test allowed topics, no scopes, operation not allowed", func(t *testing.T) {
		rt.allowedTopics = []string{"topic1"}
		a := rt.isPubSubOperationAllowed("topic2", rt.scopedPublishings)
		assert.False(t, a)
	})

	t.Run("test allowed topics, with scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = []string{"topic1"}
		rt.scopedPublishings = []string{"topic1"}
		a := rt.isPubSubOperationAllowed("topic1", rt.scopedPublishings)
		assert.True(t, a)
	})

	t.Run("topic in allowed topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		rt.allowedTopics = []string{"topic1"}
		rt.scopedPublishings = []string{"topic2"}
		a := rt.isPubSubOperationAllowed("topic1", rt.scopedPublishings)
		assert.False(t, a)
	})

	t.Run("topic in allowed topics, not in publishing scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = []string{"topic1"}
		rt.scopedPublishings = []string{}
		a := rt.isPubSubOperationAllowed("topic1", rt.scopedPublishings)
		assert.True(t, a)
	})

	t.Run("topics A and B in allowed topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		rt.allowedTopics = []string{"A", "B"}
		rt.scopedPublishings = []string{"A"}
		a := rt.isPubSubOperationAllowed("A", rt.scopedPublishings)
		assert.True(t, a)

		b := rt.isPubSubOperationAllowed("B", rt.scopedPublishings)
		assert.False(t, b)
	})
}

func TestInitSecretStores(t *testing.T) {
	t.Run("init with no store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		err := rt.initSecretStores()
		assert.Nil(t, err)
	})

	t.Run("init with store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		rt.components = append(rt.components, components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})

		err := rt.initSecretStores()
		assert.Nil(t, err)
	})

	t.Run("secret store is registered", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}),
		)

		rt.components = append(rt.components, components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})

		rt.initSecretStores()
		assert.NotNil(t, rt.secretStores["kubernetesMock"])
	})

	t.Run("get secret store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}),
		)

		rt.components = append(rt.components, components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})

		rt.initSecretStores()
		s := rt.getSecretStore("kubernetesMock")
		assert.NotNil(t, s)
	})
}

func TestMetadataItemsToPropertiesConversion(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)
	items := []components_v1alpha1.MetadataItem{
		{
			Name:  "a",
			Value: "b",
		},
	}
	m := rt.convertMetadataItemsToProperties(items)
	assert.Equal(t, 1, len(m))
	assert.Equal(t, "b", m["a"])
}

func TestProcessComponentSecrets(t *testing.T) {
	mockBinding := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "mockBinding",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type: "bindings.mock",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "a",
					SecretKeyRef: components_v1alpha1.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				},
				{
					Name:  "b",
					Value: "value2",
				},
			},
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: "kubernetes",
		},
	}

	t.Run("Standalone Mode", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = ""
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Key:  "key1",
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return m
			}),
		)

		// add Kubernetes component manually
		rt.components = append(rt.components, components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetes",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetes",
			},
		})

		rt.initSecretStores()

		mod := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value)
	})

	t.Run("Kubernetes Mode", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = ""
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Key:  "key1",
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return m
			}),
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := rt.initSecretStores()
		assert.NoError(t, err)

		mod := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value)
	})

	t.Run("Look up name only", func(t *testing.T) {
		mockBinding.Spec.Metadata[0].Value = ""
		mockBinding.Spec.Metadata[0].SecretKeyRef = components_v1alpha1.SecretKeyRef{
			Name: "name1",
		}

		rt := NewTestDaprRuntime(modes.KubernetesMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
				return m
			}),
		)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := rt.initSecretStores()
		assert.NoError(t, err)

		mod := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value)
	})
}

// Test InitSecretStore if secretstore.* refers to Kubernetes secret store
func TestInitSecretStoresInKubernetesMode(t *testing.T) {
	fakeSecretStoreWithAuth := components_v1alpha1.Component{
		ObjectMeta: meta_v1.ObjectMeta{
			Name: "fakeSecretStore",
		},
		Spec: components_v1alpha1.ComponentSpec{
			Type: "secretstores.fake.secretstore",
			Metadata: []components_v1alpha1.MetadataItem{
				{
					Name: "a",
					SecretKeyRef: components_v1alpha1.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				},
				{
					Name:  "b",
					Value: "value2",
				},
			},
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: "kubernetes",
		},
	}

	rt := NewTestDaprRuntime(modes.KubernetesMode)
	rt.components = append(rt.components, fakeSecretStoreWithAuth)

	m := NewMockKubernetesStore()
	rt.secretStoresRegistry.Register(
		secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
			return m
		}),
	)

	err := rt.initSecretStores()
	assert.NoError(t, err)
	assert.Equal(t, "value1", fakeSecretStoreWithAuth.Spec.Metadata[0].Value)
}

func TestOnNewPublishedMessage(t *testing.T) {
	testPubSubMessage := &pubsub.NewMessage{
		Topic: "topic1",
		Data:  []byte("Test Message"),
	}

	expectedRequest := &channel.InvokeRequest{
		Method:  testPubSubMessage.Topic,
		Payload: testPubSubMessage.Data,
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Post,
			tracing.CorrelationID: "",
			"headers":             fmt.Sprintf("%s%s%s", http_channel.ContentType, http_channel.HeaderEquals, pubsub.ContentType),
		},
	}

	rt := NewTestDaprRuntime(modes.StandaloneMode)

	t.Run("succeeded to publish message to user app", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("OK"),
		}

		mockAppChannel.On("InvokeMethod", expectedRequest).Return(fakeHTTPResponse, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		clientError := errors.New("Internal Error")

		fakeHTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "500"},
			Data:     []byte(clientError.Error()),
		}

		expectedClientError := fmt.Errorf("error returned from app while processing pub/sub event: Internal Error. status code returned: 500")

		mockAppChannel.On("InvokeMethod", expectedRequest).Return(fakeHTPResponse, clientError)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Equal(t, expectedClientError, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func getFakeProperties() map[string]string {
	return map[string]string{
		"host":                    "localhost",
		"password":                "fakePassword",
		"consumerID":              TestRuntimeConfigID,
		scopes.SubscriptionScopes: fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
		scopes.PublishingScopes:   fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
	}
}

func getFakeMetadataItems() []components_v1alpha1.MetadataItem {
	return []components_v1alpha1.MetadataItem{
		{
			Name:  "host",
			Value: "localhost",
		},
		{
			Name:  "password",
			Value: "fakePassword",
		},
		{
			Name:  "consumerID",
			Value: TestRuntimeConfigID,
		},
		{
			Name:  scopes.SubscriptionScopes,
			Value: fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
		},
		{
			Name:  scopes.PublishingScopes,
			Value: fmt.Sprintf("%s=topic0,topic1", TestRuntimeConfigID),
		},
	}
}

func NewTestDaprRuntime(mode modes.DaprMode) *DaprRuntime {
	testRuntimeConfig := NewRuntimeConfig(
		TestRuntimeConfigID,
		"10.10.10.12",
		"10.10.10.11",
		DefaultAllowedOrigins,
		"globalConfig",
		DefaultComponentsPath,
		string(HTTPProtocol),
		string(mode),
		DefaultDaprHTTPPort,
		0,
		DefaultDaprAPIGRPCPort,
		1024,
		DefaultProfilePort,
		false,
		-1,
		false,
		"")

	rt := NewDaprRuntime(testRuntimeConfig, &config.Configuration{})
	rt.components = []components_v1alpha1.Component{
		{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "Components",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub",
				Metadata: getFakeMetadataItems(),
			},
		},
	}

	return rt
}

func TestMTLS(t *testing.T) {
	t.Run("with mTLS enabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.runtimeConfig.mtlsEnabled = true
		rt.runtimeConfig.SentryServiceAddress = "1.1.1.1"

		os.Setenv(certs.TrustAnchorsEnvVar, testCertRoot)
		os.Setenv(certs.CertChainEnvVar, "a")
		os.Setenv(certs.CertKeyEnvVar, "b")
		defer os.Clearenv()

		certChain, err := security.GetCertChain()
		assert.Nil(t, err)
		rt.runtimeConfig.CertChain = certChain

		err = rt.establishSecurity(rt.runtimeConfig.SentryServiceAddress)
		assert.Nil(t, err)
		assert.NotNil(t, rt.authenticator)
	})

	t.Run("with mTLS disabled", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		err := rt.establishSecurity(rt.runtimeConfig.SentryServiceAddress)
		assert.Nil(t, err)
		assert.Nil(t, rt.authenticator)
	})
}

type mockBinding struct {
	hasError bool
	data     string
}

func (b *mockBinding) Init(metadata bindings.Metadata) error {
	return nil
}

func (b *mockBinding) Read(handler func(*bindings.ReadResponse) error) error {
	b.data = "test"

	err := handler(&bindings.ReadResponse{
		Metadata: map[string]string{},
		Data:     []byte(b.data),
	})
	b.hasError = err != nil
	return nil
}

func TestReadInputBindings(t *testing.T) {
	t.Run("app acknowledge, no retry", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("OK"),
		}

		mockAppChannel.On("InvokeMethod", &channel.InvokeRequest{Method: "test", Payload: []byte("test"), Metadata: map[string]string{}}).Return(fakeHTTPResponse, nil)
		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding("test", &b)

		assert.False(t, b.hasError)
	})

	t.Run("app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "500"},
			Data:     []byte("OK"),
		}

		mockAppChannel.On("InvokeMethod", &channel.InvokeRequest{Method: "test", Payload: []byte("test"), Metadata: map[string]string{}}).Return(fakeHTTPResponse, nil)
		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding("test", &b)

		assert.True(t, b.hasError)
	})

	t.Run("binding has data", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHTTPResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("OK"),
		}

		mockAppChannel.On("InvokeMethod", &channel.InvokeRequest{Method: "test", Payload: []byte("test"), Metadata: map[string]string{}}).Return(fakeHTTPResponse, nil)
		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding("test", &b)

		assert.Equal(t, "test", b.data)
	})
}

func TestNamespace(t *testing.T) {
	t.Run("empty namespace", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		ns := rt.getNamespace()

		assert.Empty(t, ns)
	})

	t.Run("non-empty namespace", func(t *testing.T) {
		os.Setenv("NAMESPACE", "a")
		defer os.Clearenv()

		rt := NewTestDaprRuntime(modes.StandaloneMode)
		ns := rt.getNamespace()

		assert.Equal(t, "a", ns)
	})
}

func TestAuthorizedComponents(t *testing.T) {
	name := "test"

	t.Run("standalone mode, no namespce", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = name

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
		assert.Equal(t, name, comps[0].Name)
	})

	t.Run("namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = "test"
		component.ObjectMeta.Namespace = "b"

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = name
		component.ObjectMeta.Namespace = "a"

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
	})

	t.Run("in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = name
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{TestRuntimeConfigID}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 1)
	})

	t.Run("not in scope, namespace match", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = name
		component.ObjectMeta.Namespace = "a"
		component.Scopes = []string{"other"}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("in scope, namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = name
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{TestRuntimeConfigID}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})

	t.Run("not in scope, namespace mismatch", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.namespace = "a"

		component := components_v1alpha1.Component{}
		component.ObjectMeta.Name = name
		component.ObjectMeta.Namespace = "b"
		component.Scopes = []string{"other"}

		comps := rt.getAuthorizedComponents([]components_v1alpha1.Component{component})
		assert.True(t, len(comps) == 0)
	})
}

type mockPublishPubSub struct {
}

// Init is a mock initialization method
func (m *mockPublishPubSub) Init(metadata pubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method
func (m *mockPublishPubSub) Publish(req *pubsub.PublishRequest) error {
	return nil
}

// Subscribe is a mock subscribe method
func (m *mockPublishPubSub) Subscribe(req pubsub.SubscribeRequest, handler func(msg *pubsub.NewMessage) error) error {
	return nil
}
