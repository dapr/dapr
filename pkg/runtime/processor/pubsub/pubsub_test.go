/*
Copyright 2024 The Dapr Authors
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

package pubsub

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	"github.com/dapr/dapr/pkg/runtime/processor/subscriber"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
	TestRuntimeConfigID  = "consumer0"
)

func TestInitPubSub(t *testing.T) {
	pubsubComponents := []componentsV1alpha1.Component{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: TestPubsubName,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub",
				Version:  "v1",
				Metadata: daprt.GetFakeMetadataItems(),
			},
		}, {
			ObjectMeta: metav1.ObjectMeta{
				Name: TestSecondPubsubName,
			},
			Spec: componentsV1alpha1.ComponentSpec{
				Type:     "pubsub.mockPubSub2",
				Version:  "v1",
				Metadata: daprt.GetFakeMetadataItems(),
			},
		},
	}

	initMockPubSubForRuntime := func() (*pubsub, *channelt.MockAppChannel, *daprt.MockPubSub, *daprt.MockPubSub) {
		mockPubSub := new(daprt.MockPubSub)
		mockPubSub2 := new(daprt.MockPubSub)

		registry := registry.New(registry.NewOptions())
		registry.PubSubs().RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
			return mockPubSub
		}, "mockPubSub")
		registry.PubSubs().RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
			return mockPubSub2
		}, "mockPubSub2")

		expectedMetadata := contribpubsub.Metadata{
			Base: metadata.Base{
				Name:       TestPubsubName,
				Properties: daprt.GetFakeProperties(),
			},
		}

		mockPubSub.On("Init", expectedMetadata).Return(nil)
		mockPubSub.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("pubsub.Handler")).Return(nil)

		expectedSecondPubsubMetadata := contribpubsub.Metadata{
			Base: metadata.Base{
				Name:       TestSecondPubsubName,
				Properties: daprt.GetFakeProperties(),
			},
		}
		mockPubSub2.On("Init", expectedSecondPubsubMetadata).Return(nil)
		mockPubSub2.On(
			"Subscribe",
			mock.AnythingOfType("pubsub.SubscribeRequest"),
			mock.AnythingOfType("pubsub.Handler")).Return(nil)

		mockAppChannel := new(channelt.MockAppChannel)
		channels := new(channels.Channels).WithAppChannel(mockAppChannel)
		compStore := compstore.New()

		resiliency := resiliency.New(logger.NewLogger("test"))
		ps := New(Options{
			Subscriber: subscriber.New(subscriber.Options{
				Channels:   channels,
				Resiliency: resiliency,
				CompStore:  compStore,
				IsHTTP:     true,
			}),
			Registry:       registry.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			ComponentStore: compStore,
		})

		return ps, mockAppChannel, mockPubSub, mockPubSub2
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		require.NoError(t, ps.subscriber.StartAppSubscriptions())

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("if not subscribing yet should not call Subscribe", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 0)
	})

	t.Run("if start subscribing then not subscribing should not call Subscribe", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.subscriber.StartAppSubscriptions())
		ps.subscriber.StopAppSubscriptions()

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 0)
	})

	t.Run("if start subscription then init, expect Subscribe", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.subscriber.StartAppSubscriptions())

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("if not subscribing yet should not call Subscribe", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			require.NoError(t, ps.Init(context.Background(), comp))
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 0)
	})

	t.Run("if start subscribing then not subscribing should not call Subscribe", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.subscriber.StartAppSubscriptions())
		ps.subscriber.StopAppSubscriptions()

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 0)
	})

	t.Run("if start subscription then init, expect Subscribe", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, mockPubSub2 := initMockPubSubForRuntime()

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString(
			[]string{"topic0", "topic1"}, // first pubsub
			[]string{"topic0"},           // second pubsub
		)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(subs).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.subscriber.StartAppSubscriptions())

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe to topic with custom route", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, _ := initMockPubSubForRuntime()

		// User App subscribes to a topic via http app channel
		sub := getSubscriptionCustom("topic0", "customroute/topic0")
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		require.NoError(t, ps.subscriber.StartAppSubscriptions())

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		ps, mockAppChannel, mockPubSub, _ := initMockPubSubForRuntime()

		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		require.NoError(t, ps.subscriber.StartAppSubscriptions())

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestConsumerID(t *testing.T) {
	metadata := []commonapi.NameValuePair{
		{
			Name: "host",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("localhost"),
				},
			},
		},
		{
			Name: "password",
			Value: commonapi.DynamicValue{
				JSON: v1.JSON{
					Raw: []byte("fakePassword"),
				},
			},
		},
	}
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: TestPubsubName,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: metadata,
		},
	}

	mockPubSub := new(daprt.MockPubSub)
	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(contribpubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		assert.Equal(t, TestRuntimeConfigID, consumerID)
	})

	mockAppChannel := new(channelt.MockAppChannel)
	channels := new(channels.Channels).WithAppChannel(mockAppChannel)

	compStore := compstore.New()

	registry := registry.New(registry.NewOptions())
	registry.PubSubs().RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
		return mockPubSub
	}, "mockPubSub")

	ps := New(Options{
		Subscriber: subscriber.New(subscriber.Options{
			Channels:   channels,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			CompStore:  compStore,
			IsHTTP:     true,
		}),
		Registry:       registry.PubSubs(),
		Meta:           meta.New(meta.Options{}),
		AppID:          TestRuntimeConfigID,
		ComponentStore: compStore,
	})

	err := ps.Init(context.Background(), pubsubComponent)
	require.NoError(t, err)
}

// helper to populate subscription array for 2 pubsubs.
// 'topics' are the topics for the first pubsub.
// 'topics2' are the topics for the second pubsub.
func getSubscriptionsJSONString(topics []string, topics2 []string) string {
	s := []runtimePubsub.SubscriptionJSON{}
	for _, t := range topics {
		s = append(s, runtimePubsub.SubscriptionJSON{
			PubsubName: TestPubsubName,
			Topic:      t,
			Routes: runtimePubsub.RoutesJSON{
				Default: t,
			},
		})
	}

	for _, t := range topics2 {
		s = append(s, runtimePubsub.SubscriptionJSON{
			PubsubName: TestSecondPubsubName,
			Topic:      t,
			Routes: runtimePubsub.RoutesJSON{
				Default: t,
			},
		})
	}
	b, _ := json.Marshal(&s)

	return string(b)
}

func getSubscriptionCustom(topic, path string) string {
	s := []runtimePubsub.SubscriptionJSON{
		{
			PubsubName: TestPubsubName,
			Topic:      topic,
			Routes: runtimePubsub.RoutesJSON{
				Default: path,
			},
		},
	}
	b, _ := json.Marshal(&s)
	return string(b)
}

func matchContextInterface(v any) bool {
	_, ok := v.(context.Context)
	return ok
}

func matchDaprRequestMethod(method string) any {
	return mock.MatchedBy(func(req *invokev1.InvokeMethodRequest) bool {
		if req == nil || req.Message() == nil || req.Message().GetMethod() != method {
			return false
		}
		return true
	})
}
