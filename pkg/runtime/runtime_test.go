package runtime

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/actionscore/actions/pkg/channel"
	http_channel "github.com/actionscore/actions/pkg/channel/http"
	channelt "github.com/actionscore/actions/pkg/channel/testing"
	"github.com/actionscore/actions/pkg/components/pubsub"
	"github.com/actionscore/actions/pkg/components/secretstores"
	"github.com/actionscore/actions/pkg/modes"

	components_v1alpha1 "github.com/actionscore/actions/pkg/apis/components/v1alpha1"
	"github.com/actionscore/actions/pkg/config"
	"github.com/stretchr/testify/assert"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TestRuntimeConfigID = "consumer0"
)

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
		},
	}, nil
}

func NewMockKubernetesStore() secretstores.SecretStore {
	return &MockKubernetesStateStore{}
}

func TestNewRuntime(t *testing.T) {
	// act
	r := NewActionsRuntime(&Config{}, &config.Configuration{})

	// assert
	assert.NotNil(t, r, "runtime must be initiated")
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestActionsRuntime(modes.StandaloneMode)

	initMockPubSubForRuntime := func(rt *ActionsRuntime) *pubsub.MockPubSub {
		mockPubSub := new(pubsub.MockPubSub)
		pubsub.RegisterMessageBus("mockPubSub", mockPubSub)

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
		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("[ \"topic0\", \"topic1\" ]"),
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "actions/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHttpResponse, nil)

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

		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "404"},
			Data:     nil,
		}

		mockAppChannel.On(
			"InvokeMethod",
			&channel.InvokeRequest{
				Method:   "actions/subscribe",
				Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Get},
			}).Return(fakeHttpResponse, nil)

		// act
		err := rt.initPubSub()

		// assert
		assert.Nil(t, err)
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestInitSecretStores(t *testing.T) {
	t.Run("init with no store", func(t *testing.T) {
		rt := NewTestActionsRuntime(modes.StandaloneMode)
		err := rt.initSecretStores()
		assert.Nil(t, err)
	})

	t.Run("init with store", func(t *testing.T) {
		rt := NewTestActionsRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		secretstores.RegisterSecretStore("kubernetesMock", m)

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
		rt := NewTestActionsRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		secretstores.RegisterSecretStore("kubernetesMock", m)

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
		rt := NewTestActionsRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		secretstores.RegisterSecretStore("kubernetesMock", m)

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
	rt := NewTestActionsRuntime(modes.StandaloneMode)
	items := []components_v1alpha1.MetadataItem{
		components_v1alpha1.MetadataItem{
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
				components_v1alpha1.MetadataItem{
					Name: "a",
					SecretKeyRef: components_v1alpha1.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				},
				components_v1alpha1.MetadataItem{
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

		rt := NewTestActionsRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		secretstores.RegisterSecretStore("kubernetes", m)

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

		rt := NewTestActionsRuntime(modes.KubernetesMode)
		m := NewMockKubernetesStore()
		secretstores.RegisterSecretStore("kubernetes", m)

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

		rt := NewTestActionsRuntime(modes.KubernetesMode)
		m := NewMockKubernetesStore()
		secretstores.RegisterSecretStore("kubernetes", m)

		// initSecretStore appends Kubernetes component even if kubernetes component is not added
		err := rt.initSecretStores()
		assert.NoError(t, err)

		mod := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "_value_data", mod.Spec.Metadata[0].Value)
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
				components_v1alpha1.MetadataItem{
					Name: "a",
					SecretKeyRef: components_v1alpha1.SecretKeyRef{
						Key:  "key1",
						Name: "name1",
					},
				},
				components_v1alpha1.MetadataItem{
					Name:  "b",
					Value: "value2",
				},
			},
		},
		Auth: components_v1alpha1.Auth{
			SecretStore: "kubernetes",
		},
	}

	rt := NewTestActionsRuntime(modes.KubernetesMode)
	rt.components = append(rt.components, fakeSecretStoreWithAuth)

	m := NewMockKubernetesStore()
	secretstores.RegisterSecretStore("kubernetes", m)

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
		Method:   testPubSubMessage.Topic,
		Payload:  testPubSubMessage.Data,
		Metadata: map[string]string{http_channel.HTTPVerb: http_channel.Post},
	}

	rt := NewTestActionsRuntime(modes.StandaloneMode)

	t.Run("succeeded to publish message to user app", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "200"},
			Data:     []byte("OK"),
		}

		mockAppChannel.On("InvokeMethod", expectedRequest).Return(fakeHttpResponse, nil)

		// act
		err := rt.onNewPublishedMessage(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeHttpResponse := &channel.InvokeResponse{
			Metadata: map[string]string{http_channel.HTTPStatusCode: "500"},
			Data:     []byte("Internal Error"),
		}

		expectedClientError := fmt.Errorf("Internal Error")

		mockAppChannel.On("InvokeMethod", expectedRequest).Return(fakeHttpResponse, expectedClientError)

		// act
		err := rt.onNewPublishedMessage(testPubSubMessage)

		// assert
		assert.Equal(t, fmt.Errorf("error from app: %s", expectedClientError), err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func getFakeProperties() map[string]string {
	return map[string]string{
		"host":       "localhost",
		"password":   "fakePassword",
		"consumerID": TestRuntimeConfigID,
	}
}

func getFakeMetadataItems() []components_v1alpha1.MetadataItem {
	return []components_v1alpha1.MetadataItem{
		components_v1alpha1.MetadataItem{
			Name:  "host",
			Value: "localhost",
		},
		components_v1alpha1.MetadataItem{
			Name:  "password",
			Value: "fakePassword",
		},
		components_v1alpha1.MetadataItem{
			Name:  "consumerID",
			Value: TestRuntimeConfigID,
		},
	}
}

func NewTestActionsRuntime(mode modes.ActionsMode) *ActionsRuntime {
	testRuntimeConfig := NewRuntimeConfig(
		TestRuntimeConfigID,
		"10.10.10.12",
		"10.10.10.11",
		DefaultAllowedOrigins,
		"globalConfig",
		DefaultComponentsPath,
		string(HTTPProtocol),
		string(mode),
		DefaultActionsHTTPPort,
		DefaultActionsGRPCPort,
		1024,
		DefaultProfilePort,
		false)

	rt := NewActionsRuntime(testRuntimeConfig, &config.Configuration{})
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
