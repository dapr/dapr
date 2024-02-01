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

package pubsub

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/dapr/components-contrib/metadata"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	commonapi "github.com/dapr/dapr/pkg/apis/common"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	subscriptionsapi "github.com/dapr/dapr/pkg/apis/subscriptions/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/expr"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	TestPubsubName       = "testpubsub"
	TestSecondPubsubName = "testpubsub2"
	resourcesDir         = "./components"
)

func TestInitPubSub(t *testing.T) {
	ps := New(Options{
		Registry:       registry.New(registry.NewOptions()).PubSubs(),
		IsHTTP:         true,
		Resiliency:     resiliency.New(logger.NewLogger("test")),
		ComponentStore: compstore.New(),
		Meta:           meta.New(meta.Options{}),
		Mode:           modes.StandaloneMode,
		Namespace:      "ns1",
		ID:             TestRuntimeConfigID,
	})

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

	initMockPubSubForRuntime := func(ps *pubsub) (*daprt.MockPubSub, *daprt.MockPubSub) {
		mockPubSub := new(daprt.MockPubSub)

		mockPubSub2 := new(daprt.MockPubSub)

		ps.registry.RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
			return mockPubSub
		}, "mockPubSub")
		ps.registry.RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
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
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
		ps.StopSubscriptions(false)
		ps.compStore.SetTopicRoutes(nil)
		ps.compStore.SetSubscriptions(nil)
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}

		return mockPubSub, mockPubSub2
	}

	t.Run("subscribe 2 topics", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("if not subscribing yet should not call Subscribe", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))
		ps.StopSubscriptions(false)

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
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

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
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))
		ps.StopSubscriptions(false)

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
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

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
		mockPubSub, _ := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("subscribe 0 topics unless user app provides topic list", func(t *testing.T) {
		mockPubSub, _ := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("get topic routes but app channel is nil", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		pst := New(Options{
			ComponentStore: compstore.New(),
			Registry:       reg.PubSubs(),
			IsHTTP:         true,
			Resiliency:     resiliency.New(logger.NewLogger("test")),
			Mode:           modes.StandaloneMode,
			Namespace:      "ns1",
			ID:             TestRuntimeConfigID,
			Channels:       new(channels.Channels),
		})
		routes, err := pst.topicRoutes(context.Background())
		require.NoError(t, err)
		assert.Empty(t, routes)
	})

	t.Run("load declarative subscription, no scopes", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		pst := New(Options{
			Registry:   reg.PubSubs(),
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Mode:       modes.StandaloneMode,
			Namespace:  "ns1",
			ID:         TestRuntimeConfigID,
			Channels:   new(channels.Channels),
		})

		require.NoError(t, os.Mkdir(resourcesDir, 0o777))
		defer os.RemoveAll(resourcesDir)

		s := testDeclarativeSubscription()

		cleanup, err := writeComponentToDisk(s, "sub.yaml")
		require.NoError(t, err)
		defer cleanup()

		pst.resourcesPath = []string{resourcesDir}
		subs := pst.declarativeSubscriptions(context.Background())
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 1) {
				assert.Equal(t, "myroute", subs[0].Rules[0].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
		}
	})

	t.Run("load declarative subscription, in scopes", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		pst := New(Options{
			Registry:   reg.PubSubs(),
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Mode:       modes.StandaloneMode,
			Namespace:  "ns1",
			ID:         TestRuntimeConfigID,
			Channels:   new(channels.Channels),
		})

		require.NoError(t, os.Mkdir(resourcesDir, 0o777))
		defer os.RemoveAll(resourcesDir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{TestRuntimeConfigID}

		cleanup, err := writeComponentToDisk(s, "sub.yaml")
		require.NoError(t, err)
		defer cleanup()

		pst.resourcesPath = []string{resourcesDir}
		subs := pst.declarativeSubscriptions(context.Background())
		if assert.Len(t, subs, 1) {
			assert.Equal(t, "topic1", subs[0].Topic)
			if assert.Len(t, subs[0].Rules, 1) {
				assert.Equal(t, "myroute", subs[0].Rules[0].Path)
			}
			assert.Equal(t, "pubsub", subs[0].PubsubName)
			assert.Equal(t, TestRuntimeConfigID, subs[0].Scopes[0])
		}
	})

	t.Run("load declarative subscription, not in scopes", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		pst := New(Options{
			Registry:   reg.PubSubs(),
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			ID:         TestRuntimeConfigID,
			Channels:   new(channels.Channels),
		})

		require.NoError(t, os.Mkdir(resourcesDir, 0o777))
		defer os.RemoveAll(resourcesDir)

		s := testDeclarativeSubscription()
		s.Scopes = []string{"scope1"}

		cleanup, err := writeComponentToDisk(s, "sub.yaml")
		require.NoError(t, err)
		defer cleanup()

		pst.resourcesPath = []string{resourcesDir}
		subs := pst.declarativeSubscriptions(context.Background())
		assert.Empty(t, subs)
	})

	t.Run("test subscribe, app allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe on protected topic with scopes", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic10"}, []string{"topic11"})
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app allowed 2 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0", "topic1"}, []string{"topic0"})
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe on 2 protected topics with scopes", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 2 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic10", "topic11"}, []string{"topic10"})
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

		require.NoError(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 2)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe, app not allowed 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic3"}, []string{"topic5"})
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
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)

		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
	})

	t.Run("test subscribe on protected topic, no scopes", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic12"}, []string{"topic12"})
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
		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 0)

		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 0)
	})

	t.Run("test subscribe, app not allowed 1 topic, allowed one topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		// topic0 is allowed, topic3 and topic5 are not
		subs := getSubscriptionsJSONString([]string{"topic0", "topic3"}, []string{"topic0", "topic5"})
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

		require.Error(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test subscribe on 2 protected topics, with scopes on 1 topic", func(t *testing.T) {
		mockPubSub, mockPubSub2 := initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		// topic0 is allowed, topic3 and topic5 are not
		subs := getSubscriptionsJSONString([]string{"topic10", "topic12"}, []string{"topic11", "topic12"})
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

		require.Error(t, ps.StartSubscriptions(context.Background()))

		// assert
		mockPubSub.AssertNumberOfCalls(t, "Init", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Init", 1)

		mockPubSub.AssertNumberOfCalls(t, "Subscribe", 1)
		mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	})

	t.Run("test bulk publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{Component: &mockPublishPubSub{}})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{Component: &mockPublishPubSub{}})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					ContentType: "text/plain",
				},
				{
					EntryId:     "2",
					Event:       []byte("test 2"),
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
	})

	t.Run("test bulk publish, topic protected, with scopes, publish succeeds", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic0"},
			ScopedPublishings: []string{"topic0"},
		})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic1"},
			ScopedPublishings: []string{"topic1"},
		})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					ContentType: "text/plain",
				},
				{
					EntryId:     "2",
					Event:       []byte("test 2"),
					ContentType: "text/plain",
				},
			},
		})

		require.NoError(t, err)
		assert.Empty(t, res.FailedEntries)
	})

	t.Run("test bulk publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("test bulk publish, topic protected, no scopes, publish fails", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		// act
		for _, comp := range pubsubComponents {
			err := ps.Init(context.Background(), comp)
			require.NoError(t, err)
		}

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})

		md := make(map[string]string, 2)
		md["key"] = "v3"
		res, err := ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		res, err = ps.BulkPublish(context.Background(), &contribpubsub.BulkPublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
			Metadata:   md,
			Entries: []contribpubsub.BulkMessageEntry{
				{
					EntryId:     "1",
					Event:       []byte("test"),
					Metadata:    md,
					ContentType: "text/plain",
				},
			},
		})
		require.Error(t, err)
		assert.Empty(t, res)
	})

	t.Run("test publish, topic allowed", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
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

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component: &mockPublishPubSub{},
		})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})

		require.NoError(t, err)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component: &mockPublishPubSub{},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})

		require.NoError(t, err)
	})

	t.Run("test publish, topic protected, with scopes, publish succeeds", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic1"})
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

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic0"},
			ScopedPublishings: []string{"topic0"},
		})
		md := make(map[string]string, 2)
		md["key"] = "v3"
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic0",
			Metadata:   md,
		})

		require.NoError(t, err)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:         &mockPublishPubSub{},
			ProtectedTopics:   []string{"topic1"},
			ScopedPublishings: []string{"topic1"},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})

		require.NoError(t, err)
	})

	t.Run("test publish, topic not allowed", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic0"})
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

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic5",
		})
		require.Error(t, err)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:     &mockPublishPubSub{},
			AllowedTopics: []string{"topic1"},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic5",
		})
		require.Error(t, err)
	})

	t.Run("test publish, topic protected, no scopes, publish fails", func(t *testing.T) {
		initMockPubSubForRuntime(ps)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		subs := getSubscriptionsJSONString([]string{"topic0"}, []string{"topic0"})
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

		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err := ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestPubsubName,
			Topic:      "topic1",
		})
		require.Error(t, err)

		ps.compStore.AddPubSub(TestSecondPubsubName, compstore.PubsubItem{
			Component:       &mockPublishPubSub{},
			ProtectedTopics: []string{"topic1"},
		})
		err = ps.Publish(context.Background(), &contribpubsub.PublishRequest{
			PubsubName: TestSecondPubsubName,
			Topic:      "topic1",
		})
		require.Error(t, err)
	})

	t.Run("test protected topics, no scopes, operation not allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("test allowed topics, no scopes, operation allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("test allowed topics, no scopes, operation not allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic2", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("test other protected topics, no allowed topics, no scopes, operation allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic2", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("test allowed topics, with scopes, operation allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic1"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("test protected topics, with scopes, operation allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic1"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("topic in allowed topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic2"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("topic in protected topics, not in existing publishing scopes, operation not allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{"topic2"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("topic in allowed topics, not in publishing scopes, operation allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"topic1"}, ScopedPublishings: []string{}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.True(t, a)
	})

	t.Run("topic in protected topics, not in publishing scopes, operation not allowed", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"topic1"}, ScopedPublishings: []string{}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "topic1", pubSub.ScopedPublishings)
		assert.False(t, a)
	})

	t.Run("topics A and B in allowed topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{AllowedTopics: []string{"A", "B"}, ScopedPublishings: []string{"A"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "A", pubSub.ScopedPublishings)
		assert.True(t, a)
		b := ps.isOperationAllowed(TestPubsubName, "B", pubSub.ScopedPublishings)
		assert.False(t, b)
	})

	t.Run("topics A and B in protected topics, A in publishing scopes, operation allowed for A only", func(t *testing.T) {
		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{ProtectedTopics: []string{"A", "B"}, ScopedPublishings: []string{"A"}})
		pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
		require.True(t, ok)
		a := ps.isOperationAllowed(TestPubsubName, "A", pubSub.ScopedPublishings)
		assert.True(t, a)
		b := ps.isOperationAllowed(TestPubsubName, "B", pubSub.ScopedPublishings)
		assert.False(t, b)
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

	reg := registry.New(registry.NewOptions())
	ps := New(Options{
		Meta:           meta.New(meta.Options{}),
		ComponentStore: compstore.New(),
		Registry:       reg.PubSubs(),
		IsHTTP:         true,
		Resiliency:     resiliency.New(logger.NewLogger("test")),
		Namespace:      "ns1",
		ID:             TestRuntimeConfigID,
	})
	mockPubSub := new(daprt.MockPubSub)

	reg.PubSubs().RegisterComponent(
		func(_ logger.Logger) contribpubsub.PubSub {
			return mockPubSub
		},
		"mockPubSub",
	)

	mockPubSub.On("Init", mock.Anything).Return(nil).Run(func(args mock.Arguments) {
		metadata := args.Get(0).(contribpubsub.Metadata)
		consumerID := metadata.Properties["consumerID"]
		assert.Equal(t, TestRuntimeConfigID, consumerID)
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

func testDeclarativeSubscription() subscriptionsapi.Subscription {
	return subscriptionsapi.Subscription{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Subscription",
			APIVersion: "v1alpha1",
		},
		Spec: subscriptionsapi.SubscriptionSpec{
			Topic:      "topic1",
			Route:      "myroute",
			Pubsubname: "pubsub",
		},
	}
}

// writeComponentToDisk the given content into a file inside components directory.
func writeComponentToDisk(content any, fileName string) (cleanup func(), error error) {
	filePath := fmt.Sprintf("%s/%s", resourcesDir, fileName)
	b, err := yaml.Marshal(content)
	if err != nil {
		return nil, err
	}
	return func() {
		os.Remove(filePath)
	}, os.WriteFile(filePath, b, 0o600)
}

func TestNamespacedPublisher(t *testing.T) {
	reg := registry.New(registry.NewOptions())
	ps := New(Options{
		Meta:           meta.New(meta.Options{}),
		ComponentStore: compstore.New(),
		Registry:       reg.PubSubs(),
		IsHTTP:         true,
		Resiliency:     resiliency.New(logger.NewLogger("test")),
		Namespace:      "ns1",
		ID:             TestRuntimeConfigID,
	})

	for name := range ps.compStore.ListPubSubs() {
		ps.compStore.DeletePubSub(name)
	}
	ps.compStore.AddPubSub(TestPubsubName, compstore.PubsubItem{
		Component:       &mockPublishPubSub{},
		NamespaceScoped: true,
	})
	ps.Publish(context.Background(), &contribpubsub.PublishRequest{
		PubsubName: TestPubsubName,
		Topic:      "topic0",
	})

	pubSub, ok := ps.compStore.GetPubSub(TestPubsubName)
	require.True(t, ok)
	assert.Equal(t, "ns1topic0", pubSub.Component.(*mockPublishPubSub).PublishedRequest.Load().Topic)
}

type mockPublishPubSub struct {
	PublishedRequest atomic.Pointer[contribpubsub.PublishRequest]
}

// Init is a mock initialization method.
func (m *mockPublishPubSub) Init(ctx context.Context, metadata contribpubsub.Metadata) error {
	return nil
}

// Publish is a mock publish method.
func (m *mockPublishPubSub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	m.PublishedRequest.Store(req)
	return nil
}

// BulkPublish is a mock bulk publish method returning a success all the time.
func (m *mockPublishPubSub) BulkPublish(req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	return contribpubsub.BulkPublishResponse{}, nil
}

func (m *mockPublishPubSub) BulkSubscribe(ctx context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.BulkHandler) (contribpubsub.BulkSubscribeResponse, error) {
	return contribpubsub.BulkSubscribeResponse{}, nil
}

// Subscribe is a mock subscribe method.
func (m *mockPublishPubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	return nil
}

func (m *mockPublishPubSub) Close() error {
	return nil
}

func (m *mockPublishPubSub) Features() []contribpubsub.Feature {
	return nil
}

func TestPubsubWithResiliency(t *testing.T) {
	ps := New(Options{
		Registry:       registry.New(registry.NewOptions()).PubSubs(),
		IsHTTP:         true,
		Resiliency:     resiliency.FromConfigurations(logger.NewLogger("test"), daprt.TestResiliency),
		ComponentStore: compstore.New(),
		Meta:           meta.New(meta.Options{}),
		Mode:           modes.StandaloneMode,
		Namespace:      "ns1",
		ID:             TestRuntimeConfigID,
	})

	failingPubsub := daprt.FailingPubsub{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingTopic": 1,
			},
			map[string]time.Duration{
				"timeoutTopic": time.Second * 10,
			},
			map[string]int{},
		),
	}

	failingAppChannel := daprt.FailingAppChannel{
		Failure: daprt.NewFailure(
			map[string]int{
				"failingSubTopic": 1,
			},
			map[string]time.Duration{
				"timeoutSubTopic": time.Second * 10,
			},
			map[string]int{},
		),
		KeyFunc: func(req *invokev1.InvokeMethodRequest) string {
			rawData, _ := io.ReadAll(req.RawData())
			data := make(map[string]string)
			json.Unmarshal(rawData, &data)
			val, _ := base64.StdEncoding.DecodeString(data["data_base64"])
			return string(val)
		},
	}

	ps.registry.RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub { return &failingPubsub }, "failingPubsub")

	component := componentsV1alpha1.Component{}
	component.ObjectMeta.Name = "failPubsub"
	component.Spec.Type = "pubsub.failingPubsub"

	err := ps.Init(context.TODO(), component)
	require.NoError(t, err)

	t.Run("pubsub publish retries with resiliency", func(t *testing.T) {
		req := &contribpubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "failingTopic",
		}
		err := ps.Publish(context.Background(), req)

		require.NoError(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("failingTopic"))
	})

	t.Run("pubsub publish times out with resiliency", func(t *testing.T) {
		req := &contribpubsub.PublishRequest{
			PubsubName: "failPubsub",
			Topic:      "timeoutTopic",
		}

		start := time.Now()
		err := ps.Publish(context.Background(), req)
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingPubsub.Failure.CallCount("timeoutTopic"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})

	ps.isHTTP = true
	ps.channels = new(channels.Channels).WithAppChannel(&failingAppChannel)

	t.Run("pubsub retries subscription event with resiliency", func(t *testing.T) {
		ps.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
			"failPubsub": map[string]compstore.TopicRouteElem{
				"failingSubTopic": {
					Metadata: map[string]string{
						"rawPayload": "true",
					},
					Rules: []*runtimePubsub.Rule{
						{
							Path: "failingPubsub",
						},
					},
				},
			},
		})

		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub("failPubsub", compstore.PubsubItem{Component: &failingPubsub})

		ps.topicCancels = map[string]context.CancelFunc{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := ps.beginPubSub(ctx, "failPubsub")

		require.NoError(t, err)
		assert.Equal(t, 2, failingAppChannel.Failure.CallCount("failingSubTopic"))
	})

	t.Run("pubsub times out sending event to app with resiliency", func(t *testing.T) {
		ps.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
			"failPubsub": map[string]compstore.TopicRouteElem{
				"timeoutSubTopic": {
					Metadata: map[string]string{
						"rawPayload": "true",
					},
					Rules: []*runtimePubsub.Rule{
						{
							Path: "failingPubsub",
						},
					},
				},
			},
		})

		for name := range ps.compStore.ListPubSubs() {
			ps.compStore.DeletePubSub(name)
		}
		ps.compStore.AddPubSub("failPubsub", compstore.PubsubItem{Component: &failingPubsub})

		ps.topicCancels = map[string]context.CancelFunc{}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		start := time.Now()
		err := ps.beginPubSub(ctx, "failPubsub")
		end := time.Now()

		require.Error(t, err)
		assert.Equal(t, 2, failingAppChannel.Failure.CallCount("timeoutSubTopic"))
		assert.Less(t, end.Sub(start), time.Second*10)
	})
}

func TestPubsubLifecycle(t *testing.T) {
	ps := New(Options{
		Registry:       registry.New(registry.NewOptions()).PubSubs(),
		IsHTTP:         true,
		Resiliency:     resiliency.New(logger.NewLogger("test")),
		ComponentStore: compstore.New(),
		Meta:           meta.New(meta.Options{}),
		Mode:           modes.StandaloneMode,
		Namespace:      "ns1",
		ID:             TestRuntimeConfigID,
	})

	comp1 := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub1",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSubAlpha",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		},
	}
	comp2 := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub2",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSubBeta",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		},
	}
	comp3 := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: "mockPubSub3",
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSubBeta",
			Version:  "v1",
			Metadata: []commonapi.NameValuePair{},
		},
	}

	ps.registry.RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
		c := new(daprt.InMemoryPubsub)
		c.On("Init", mock.AnythingOfType("pubsub.Metadata")).Return(nil)
		return c
	}, "mockPubSubAlpha")
	ps.registry.RegisterComponent(func(_ logger.Logger) contribpubsub.PubSub {
		c := new(daprt.InMemoryPubsub)
		c.On("Init", mock.AnythingOfType("pubsub.Metadata")).Return(nil)
		return c
	}, "mockPubSubBeta")

	err := ps.Init(context.Background(), comp1)
	require.NoError(t, err)
	err = ps.Init(context.Background(), comp2)
	require.NoError(t, err)
	err = ps.Init(context.Background(), comp3)
	require.NoError(t, err)

	forEachPubSub := func(f func(name string, comp *daprt.InMemoryPubsub)) int {
		i := 0
		for name, ps := range ps.compStore.ListPubSubs() {
			f(name, ps.Component.(*daprt.InMemoryPubsub))
			i++
		}
		return i
	}
	getPubSub := func(name string) *daprt.InMemoryPubsub {
		pubSub, ok := ps.compStore.GetPubSub(name)
		require.True(t, ok)
		return pubSub.Component.(*daprt.InMemoryPubsub)
	}

	done := forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
		comp.AssertNumberOfCalls(t, "Init", 1)
	})
	require.Equal(t, 3, done)

	subscriptions := make(map[string][]string)
	messages := make(map[string][]*contribpubsub.NewMessage)
	var (
		subscriptionsCh  chan struct{}
		messagesCh       chan struct{}
		subscriptionsMux sync.Mutex
		msgMux           sync.Mutex
	)
	forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
		comp.SetOnSubscribedTopicsChanged(func(topics []string) {
			sort.Strings(topics)
			subscriptionsMux.Lock()
			subscriptions[name] = topics
			subscriptionsMux.Unlock()
			if subscriptionsCh != nil {
				subscriptionsCh <- struct{}{}
			}
		})
		comp.SetHandler(func(topic string, msg *contribpubsub.NewMessage) {
			msgMux.Lock()
			if messages[name+"|"+topic] == nil {
				messages[name+"|"+topic] = []*contribpubsub.NewMessage{msg}
			} else {
				messages[name+"|"+topic] = append(messages[name+"|"+topic], msg)
			}
			if messagesCh != nil {
				messagesCh <- struct{}{}
			}
			msgMux.Unlock()
		})
	})

	setTopicRoutes := func() {
		ps.compStore.SetTopicRoutes(map[string]compstore.TopicRoutes{
			"mockPubSub1": {
				"topic1": {
					Metadata: map[string]string{"rawPayload": "true"},
					Rules:    []*runtimePubsub.Rule{{Path: "topic1"}},
				},
			},
			"mockPubSub2": {
				"topic2": {
					Metadata: map[string]string{"rawPayload": "true"},
					Rules:    []*runtimePubsub.Rule{{Path: "topic2"}},
				},
				"topic3": {
					Metadata: map[string]string{"rawPayload": "true"},
					Rules:    []*runtimePubsub.Rule{{Path: "topic3"}},
				},
			},
		})

		forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
			comp.Calls = nil
			comp.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
		})
	}

	resetState := func() {
		messages = make(map[string][]*contribpubsub.NewMessage)
		forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
			comp.Calls = nil
		})
	}

	subscribePredefined := func(t *testing.T) {
		setTopicRoutes()

		subscriptionsCh = make(chan struct{}, 5)
		require.NoError(t, ps.StartSubscriptions(context.Background()))

		done := forEachPubSub(func(name string, comp *daprt.InMemoryPubsub) {
			switch name {
			case "mockPubSub1":
				comp.AssertNumberOfCalls(t, "Subscribe", 1)
			case "mockPubSub2":
				comp.AssertNumberOfCalls(t, "Subscribe", 2)
			case "mockPubSub3":
				comp.AssertNumberOfCalls(t, "Subscribe", 0)
			}
		})
		require.Equal(t, 3, done)

		for i := 0; i < 3; i++ {
			<-subscriptionsCh
		}
		close(subscriptionsCh)
		subscriptionsCh = nil

		assert.True(t, reflect.DeepEqual(subscriptions["mockPubSub1"], []string{"topic1"}),
			"expected %v to equal %v", subscriptions["mockPubSub1"], []string{"topic1"})
		assert.True(t, reflect.DeepEqual(subscriptions["mockPubSub2"], []string{"topic2", "topic3"}),
			"expected %v to equal %v", subscriptions["mockPubSub2"], []string{"topic2", "topic3"})
		assert.Empty(t, subscriptions["mockPubSub3"])
	}

	t.Run("subscribe to 3 topics on 2 components", subscribePredefined)

	ciao := []byte("ciao")
	sendMessages := func(t *testing.T, expect int) {
		send := []contribpubsub.PublishRequest{
			{Data: ciao, PubsubName: "mockPubSub1", Topic: "topic1"},
			{Data: ciao, PubsubName: "mockPubSub2", Topic: "topic2"},
			{Data: ciao, PubsubName: "mockPubSub2", Topic: "topic3"},
			{Data: ciao, PubsubName: "mockPubSub3", Topic: "topic4"},
			{Data: ciao, PubsubName: "mockPubSub3", Topic: "not-subscribed"},
		}

		msgMux.Lock()
		messagesCh = make(chan struct{}, expect+2)
		msgMux.Unlock()

		for _, m := range send {
			//nolint:gosec
			err = ps.Publish(context.Background(), &m)
			require.NoError(t, err)
		}

		for i := 0; i < expect; i++ {
			<-messagesCh
		}

		// Sleep to ensure no new messages have come in
		time.Sleep(10 * time.Millisecond)
		assert.Empty(t, messagesCh)

		msgMux.Lock()
		close(messagesCh)
		messagesCh = nil
		msgMux.Unlock()
	}

	t.Run("messages are delivered to 3 topics", func(t *testing.T) {
		resetState()
		sendMessages(t, 3)

		require.Len(t, messages, 3)
		_ = assert.Len(t, messages["mockPubSub1|topic1"], 1) &&
			assert.Equal(t, messages["mockPubSub1|topic1"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub2|topic2"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic2"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
	})

	t.Run("unsubscribe from mockPubSub2/topic2", func(t *testing.T) {
		resetState()
		comp := getPubSub("mockPubSub2")
		comp.On("unsubscribed", "topic2").Return(nil).Once()

		ps.unsubscribeTopic("mockPubSub2||topic2")

		sendMessages(t, 2)

		require.Len(t, messages, 2)
		_ = assert.Len(t, messages["mockPubSub1|topic1"], 1) &&
			assert.Equal(t, messages["mockPubSub1|topic1"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
		comp.AssertCalled(t, "unsubscribed", "topic2")
	})

	t.Run("unsubscribe from mockPubSub1/topic1", func(t *testing.T) {
		resetState()
		comp := getPubSub("mockPubSub1")
		comp.On("unsubscribed", "topic1").Return(nil).Once()

		ps.unsubscribeTopic("mockPubSub1||topic1")

		sendMessages(t, 1)

		require.Len(t, messages, 1)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
		comp.AssertCalled(t, "unsubscribed", "topic1")
	})

	t.Run("subscribe to mockPubSub3/topic4", func(t *testing.T) {
		resetState()

		err = ps.subscribeTopic("mockPubSub3", "topic4", compstore.TopicRouteElem{})
		require.NoError(t, err)

		sendMessages(t, 2)

		require.Len(t, messages, 2)
		_ = assert.Len(t, messages["mockPubSub2|topic3"], 1) &&
			assert.Equal(t, messages["mockPubSub2|topic3"][0].Data, ciao)
		_ = assert.Len(t, messages["mockPubSub3|topic4"], 1) &&
			assert.Equal(t, messages["mockPubSub3|topic4"][0].Data, ciao)
	})

	t.Run("stopSubscriptions", func(t *testing.T) {
		resetState()

		comp2 := getPubSub("mockPubSub2")
		comp2.On("unsubscribed", "topic3").Return(nil).Once()
		comp3 := getPubSub("mockPubSub3")
		comp3.On("unsubscribed", "topic4").Return(nil).Once()

		ps.StopSubscriptions(false)

		sendMessages(t, 0)

		require.Empty(t, messages)
		comp2.AssertCalled(t, "unsubscribed", "topic3")
		comp3.AssertCalled(t, "unsubscribed", "topic4")
	})

	t.Run("restart subscriptions with pre-defined ones", subscribePredefined)

	t.Run("shutdown", func(t *testing.T) {
		resetState()

		comp1 := getPubSub("mockPubSub1")
		comp1.On("unsubscribed", "topic1").Return(nil).Once()
		comp2 := getPubSub("mockPubSub2")
		comp2.On("unsubscribed", "topic2").Return(nil).Once()
		comp2.On("unsubscribed", "topic3").Return(nil).Once()

		ps.StopSubscriptions(false)
		time.Sleep(time.Second / 2)

		comp1.AssertCalled(t, "unsubscribed", "topic1")
		comp2.AssertCalled(t, "unsubscribed", "topic2")
		comp2.AssertCalled(t, "unsubscribed", "topic3")
	})
}

func TestFindMatchingRoute(t *testing.T) {
	r, err := createRoutingRule(`event.type == "MyEventType"`, "mypath")
	require.NoError(t, err)
	rules := []*runtimePubsub.Rule{r}
	path, shouldProcess, err := findMatchingRoute(rules, map[string]interface{}{
		"type": "MyEventType",
	})
	require.NoError(t, err)
	assert.Equal(t, "mypath", path)
	assert.True(t, shouldProcess)
}

func createRoutingRule(match, path string) (*runtimePubsub.Rule, error) {
	var e *expr.Expr
	matchTrimmed := strings.TrimSpace(match)
	if matchTrimmed != "" {
		e = &expr.Expr{}
		if err := e.DecodeString(matchTrimmed); err != nil {
			return nil, err
		}
	}

	return &runtimePubsub.Rule{
		Match: e,
		Path:  path,
	}, nil
}
