// ------------------------------------------------------------
// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
// ------------------------------------------------------------

package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/dapr/components-contrib/bindings"
	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/components-contrib/secretstores"
	components_v1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	pubsub_loader "github.com/dapr/dapr/pkg/components/pubsub"
	secretstores_loader "github.com/dapr/dapr/pkg/components/secretstores"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	runtime_pubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/security"
	"github.com/dapr/dapr/pkg/scopes"
	"github.com/dapr/dapr/pkg/sentry/certs"
	daprt "github.com/dapr/dapr/pkg/testing"
	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	meta_v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	TestRuntimeConfigID  = "consumer0"
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
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

// helper to populate subscription array for 2 pubsubs.
// 'topics' are the topics for the first pubsub.
// 'topics2' are the topics for the second pubsub.
func getSubscriptionsJSONString(topics []string, topics2 []string) string {
	s := []runtime_pubsub.Subscription{}
	for _, t := range topics {
		s = append(s, runtime_pubsub.Subscription{
			PubsubName: TestPubsubName,
			Topic:      t,
			Route:      t,
		})
	}

	for _, t := range topics2 {
		s = append(s, runtime_pubsub.Subscription{
			PubsubName: TestSecondPubsubName,
			Topic:      t,
			Route:      t,
		})
	}
	b, _ := json.Marshal(&s)

	return string(b)
}

func getSubscriptionCustom(topic, route string) string {
	s := []runtime_pubsub.Subscription{
		{
			PubsubName: TestPubsubName,
			Topic:      topic,
			Route:      route,
		},
	}
	b, _ := json.Marshal(&s)
	return string(b)
}

func TestInitPubSub(t *testing.T) {
	rt := NewTestDaprRuntime(modes.StandaloneMode)

	pubsubComponents := []components_v1alpha1.Component{
		{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: TestPubsubName,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub",
				Metadata: getFakeMetadataItems(),
			},
		}, {
			ObjectMeta: meta_v1.ObjectMeta{
				Name: TestSecondPubsubName,
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub2",
				Metadata: getFakeMetadataItems(),
			},
		},
	}

	initMockPubSubForRuntime := func(rt *DaprRuntime) (*daprt.MockPubSub, *daprt.MockPubSub) {
		mockPubSub := new(daprt.MockPubSub)

		mockPubSub2 := new(daprt.MockPubSub)

		rt.pubSubRegistry.Register(
			pubsub_loader.New("mockPubSub", func() pubsub.PubSub {
				return mockPubSub
			}),

			pubsub_loader.New("mockPubSub2", func() pubsub.PubSub {
				return mockPubSub2
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

		mockPubSub2.On("Init", expectedMetadata).Return(nil)
		mockPubSub2.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("func(*pubsub.NewMessage) error")).Return(nil)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel
		rt.topicRoutes = nil
		rt.pubSubs = make(map[string]pubsub.PubSub)

		return mockPubSub, mockPubSub2
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 2 topics via http app channel
		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"})           // second pubsub
		fakeResp.WithRawData([]byte(subs), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe to topic with custom route", func(t *testing.T) {
		mockPubSub, _ := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes to a topic via http app channel
		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		sub := getSubscriptionCustom("topic0", "customroute/topic0")
		fakeResp.WithRawData([]byte(sub), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		mockPubSub, _ := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")
		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil)

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
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
		rt.pubSubs[TestPubsubName], _ = initMockPubSubForRuntime(rt)
		a := rt.getPublishAdapter()
		assert.NotNil(t, a)
	})

	t.Run("test subscribe, app allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app allowed 2 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 2 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0", "topic1"}, []string{"topic0"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app not allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic3"}, []string{"topic5"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)

		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
	})

	t.Run("test subscribe, app not allowed 1 topic, allowed one topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)

		// topic0 is allowed, topic3 and topic5 are not
		subs := getSubscriptionsJSONString([]string{"topic0", "topic3"}, []string{"topic0", "topic5"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		rt.pubSubs[TestPubsubName] = &mockPublishPubSub{}
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
		})

		assert.Nil(t, err)

		rt.pubSubs[TestSecondPubsubName] = &mockPublishPubSub{}
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})

		assert.Nil(t, err)
	})

	t.Run("test publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(rt)

		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("dapr/subscribe")
		fakeReq.WithHTTPExtension(http.MethodGet, "")
		fakeReq.WithRawData(nil, "application/json")

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic0"})
		fakeResp.WithRawData([]byte(subs), "application/json")
		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.emptyCtx"), fakeReq).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		rt.pubSubs[TestPubsubName] = &mockPublishPubSub{}
		err := rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
		})
		assert.NotNil(t, err)

		rt.pubSubs[TestPubsubName] = &mockPublishPubSub{}
		err = rt.Publish(&pubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
		})
		assert.NotNil(t, err)
	})

	t.Run("test allowed topics, no scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)
	})

	t.Run("test allowed topics, no scopes, operation not allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic2", rt.scopedPublishings[TestPubsubName])
		assert.False(t, a)
	})

	t.Run("test allowed topics, with scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		rt.scopedPublishings = map[string][]string{TestPubsubName: {"topic1"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)
	})

	t.Run("topic in allowed topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		rt.scopedPublishings = map[string][]string{TestPubsubName: {"topic2"}}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.False(t, a)
	})

	t.Run("topic in allowed topics, not in publishing scopes, operation allowed", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"topic1"}}
		rt.scopedPublishings = map[string][]string{}
		a := rt.isPubSubOperationAllowed(TestPubsubName, "topic1", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)
	})

	t.Run("topics A and B in allowed topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		rt.allowedTopics = map[string][]string{TestPubsubName: {"A", "B"}}
		rt.scopedPublishings = map[string][]string{TestPubsubName: {"A"}}

		a := rt.isPubSubOperationAllowed(TestPubsubName, "A", rt.scopedPublishings[TestPubsubName])
		assert.True(t, a)

		b := rt.isPubSubOperationAllowed(TestPubsubName, "B", rt.scopedPublishings[TestPubsubName])
		assert.False(t, b)
	})
}

func TestInitSecretStores(t *testing.T) {
	t.Run("init with store", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		err := rt.processOneComponent(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})
		assert.Nil(t, err)
	})

	t.Run("secret store is registered", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		m := NewMockKubernetesStore()
		rt.secretStoresRegistry.Register(
			secretstores_loader.New("kubernetesMock", func() secretstores.SecretStore {
				return m
			}))

		rt.processOneComponent(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})
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

		rt.processOneComponent(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetesMock",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetesMock",
			},
		})

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
		rt.processOneComponent(components_v1alpha1.Component{
			ObjectMeta: meta_v1.ObjectMeta{
				Name: "kubernetes",
			},
			Spec: components_v1alpha1.ComponentSpec{
				Type: "secretstores.kubernetes",
			},
		})

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value)
		assert.Empty(t, unready)
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
		for _, comp := range rt.builtinSecretStore() {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value)
		assert.Empty(t, unready)
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
		for _, comp := range rt.builtinSecretStore() {
			err := rt.processOneComponent(comp)
			assert.Nil(t, err)
		}

		mod, unready := rt.processComponentSecrets(mockBinding)
		assert.Equal(t, "value1", mod.Spec.Metadata[0].Value)
		assert.Empty(t, unready)
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

	m := NewMockKubernetesStore()
	rt.secretStoresRegistry.Register(
		secretstores_loader.New("kubernetes", func() secretstores.SecretStore {
			return m
		}),
	)
	for _, comp := range rt.builtinSecretStore() {
		err := rt.processOneComponent(comp)
		assert.Nil(t, err)
	}
	fakeSecretStoreWithAuth, _ = rt.processComponentSecrets(fakeSecretStoreWithAuth)
	// initSecretStore appends Kubernetes component even if kubernetes component is not added
	assert.Equal(t, "value1", fakeSecretStoreWithAuth.Spec.Metadata[0].Value)
}

func TestOnNewPublishedMessage(t *testing.T) {
	topic := "topic1"

	envelope := pubsub.NewCloudEventsEnvelope("", "", pubsub.DefaultCloudEventType, "", topic, TestPubsubName, []byte("Test Message"))
	b, err := json.Marshal(envelope)
	assert.Nil(t, err)

	testPubSubMessage := &pubsub.NewMessage{
		Topic: topic,
		Data:  b,
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.Topic)
	fakeReq.WithHTTPExtension(http.MethodPost, "")
	fakeReq.WithRawData(testPubSubMessage.Data, pubsub.ContentType)

	rt := NewTestDaprRuntime(modes.StandaloneMode)
	rt.topicRoutes = map[string]TopicRoute{}
	rt.topicRoutes[TestPubsubName] = TopicRoute{routes: make(map[string]string)}
	rt.topicRoutes[TestPubsubName].routes["topic1"] = "topic1"

	t.Run("succeeded to publish message to user app with non-json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("OK"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with status", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"SUCCESS\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask for retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"RETRY\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		var cloudEvent pubsub.CloudEventsEnvelope
		json := jsoniter.ConfigFastest
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := fmt.Errorf("RETRY status returned from app while processing pub/sub event %v", cloudEvent.ID)
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask to drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"DROP\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		assert.Nil(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned unknown status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("{ \"status\": \"not_valid\"}"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		var cloudEvent pubsub.CloudEventsEnvelope
		json := jsoniter.ConfigFastest
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := fmt.Errorf("unknown status returned from app while processing pub/sub event %v: not_valid", cloudEvent.ID)
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 500", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		clientError := errors.New("Internal Error")
		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil)
		fakeResp.WithRawData([]byte(clientError.Error()), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		// act
		err := rt.publishMessageHTTP(testPubSubMessage)

		// assert
		var cloudEvent pubsub.CloudEventsEnvelope
		json := jsoniter.ConfigFastest
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := fmt.Errorf("retriable error returned from app while processing pub/sub event %v: Internal Error. status code returned: 500", cloudEvent.ID)
		assert.Equal(t, expectedClientError.Error(), err.Error())
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
		"",
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
	metadata map[string]string
}

func (b *mockBinding) Init(metadata bindings.Metadata) error {
	return nil
}

func (b *mockBinding) Read(handler func(*bindings.ReadResponse) error) error {
	b.data = "test"
	metadata := map[string]string{}
	if b.metadata != nil {
		metadata = b.metadata
	}

	err := handler(&bindings.ReadResponse{
		Metadata: metadata,
		Data:     []byte(b.data),
	})
	b.hasError = err != nil
	return nil
}

func (b *mockBinding) Operations() []bindings.OperationKind {
	return []bindings.OperationKind{"create"}
}

func (b *mockBinding) Invoke(req *bindings.InvokeRequest) (*bindings.InvokeResponse, error) {
	return nil, nil
}

func TestInvokeOutputBindings(t *testing.T) {
	t.Run("output binding missing operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data: []byte(""),
		})
		assert.NotNil(t, err)
		assert.Equal(t, "operation field is missing from request", err.Error())
	})

	t.Run("output binding valid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.outputBindings["mockBinding"] = &mockBinding{}

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.CreateOperation,
		})
		assert.Nil(t, err)
	})

	t.Run("output binding invalid operation", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		rt.outputBindings["mockBinding"] = &mockBinding{}

		_, err := rt.sendToOutputBinding("mockBinding", &bindings.InvokeRequest{
			Data:      []byte(""),
			Operation: bindings.GetOperation,
		})
		assert.NotNil(t, err)
		assert.Equal(t, "binding mockBinding does not support operation get. supported operations: create", err.Error())
	})
}

func TestReadInputBindings(t *testing.T) {
	t.Run("app acknowledge, no retry", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("test")
		fakeReq.WithHTTPExtension(http.MethodPost, "")
		fakeReq.WithRawData([]byte("test"), "application/json")
		fakeReq.WithMetadata(map[string][]string{})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("OK"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding("test", &b)

		assert.False(t, b.hasError)
	})

	t.Run("app returns error", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("test")
		fakeReq.WithHTTPExtension(http.MethodPost, "")
		fakeReq.WithRawData([]byte("test"), "application/json")
		fakeReq.WithMetadata(map[string][]string{})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil)
		fakeResp.WithRawData([]byte("Internal Error"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)

		rt.appChannel = mockAppChannel

		b := mockBinding{}
		rt.readFromBinding("test", &b)

		assert.True(t, b.hasError)
	})

	t.Run("binding has data and metadata", func(t *testing.T) {
		rt := NewTestDaprRuntime(modes.StandaloneMode)
		mockAppChannel := new(channelt.MockAppChannel)
		rt.appChannel = mockAppChannel

		fakeReq := invokev1.NewInvokeMethodRequest("test")
		fakeReq.WithHTTPExtension(http.MethodPost, "")
		fakeReq.WithRawData([]byte("test"), "application/json")
		fakeReq.WithMetadata(map[string][]string{"bindings": {"input"}})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		fakeResp.WithRawData([]byte("OK"), "application/json")

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.valueCtx"), fakeReq).Return(fakeResp, nil)
		rt.appChannel = mockAppChannel

		b := mockBinding{metadata: map[string]string{"bindings": "input"}}
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
