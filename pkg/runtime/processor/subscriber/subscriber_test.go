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

package subscriber

import (
	"context"
	"encoding/json"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

const (
	TestRuntimeConfigID = "consumer0"
	TestPubsubName      = "testpubsub"
)

func TestSubscriptionLifecycle(t *testing.T) {
	mockPubSub1 := new(daprt.InMemoryPubsub)
	mockPubSub2 := new(daprt.InMemoryPubsub)
	mockPubSub3 := new(daprt.InMemoryPubsub)

	mockPubSub1.On("Init", mock.Anything).Return(nil)
	mockPubSub2.On("Init", mock.Anything).Return(nil)
	mockPubSub3.On("Init", mock.Anything).Return(nil)
	mockPubSub1.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub2.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub3.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub1.On("unsubscribed", "topic1").Return(nil)
	mockPubSub2.On("unsubscribed", "topic2").Return(nil)
	mockPubSub3.On("unsubscribed", "topic3").Return(nil)
	require.NoError(t, mockPubSub1.Init(context.Background(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub2.Init(context.Background(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub3.Init(context.Background(), contribpubsub.Metadata{}))

	compStore := compstore.New()
	compStore.AddPubSub("mockPubSub1", &rtpubsub.PubsubItem{
		Component: mockPubSub1,
	})
	compStore.AddPubSub("mockPubSub2", &rtpubsub.PubsubItem{
		Component: mockPubSub2,
	})
	compStore.AddPubSub("mockPubSub3", &rtpubsub.PubsubItem{
		Component: mockPubSub3,
	})

	compStore.SetProgramaticSubscriptions(
		rtpubsub.Subscription{
			PubsubName: "mockPubSub1",
			Topic:      "topic1",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		},
		rtpubsub.Subscription{
			PubsubName: "mockPubSub2",
			Topic:      "topic2",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		},
		rtpubsub.Subscription{
			PubsubName: "mockPubSub3",
			Topic:      "topic3",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		},
	)

	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub1",
			Topic:      "topic4",
			Routes:     subapi.Routes{Default: "/"},
		},
	})
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub2||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub2",
			Topic:      "topic5",
			Routes:     subapi.Routes{Default: "/"},
		},
	})
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub3||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub3",
			Topic:      "topic6",
			Routes:     subapi.Routes{Default: "/"},
		},
	})

	subs := New(Options{
		CompStore:  compStore,
		IsHTTP:     true,
		Resiliency: resiliency.New(logger.NewLogger("test")),
		Namespace:  "ns1",
		AppID:      TestRuntimeConfigID,
		Channels:   new(channels.Channels).WithAppChannel(new(channelt.MockAppChannel)),
	})
	subs.hasInitProg = true

	gotTopics := make([][]string, 3)
	changeCalled := make([]atomic.Int32, 3)
	mockPubSub1.SetOnSubscribedTopicsChanged(func(topics []string) {
		gotTopics[0] = topics
		changeCalled[0].Add(1)
	})
	mockPubSub2.SetOnSubscribedTopicsChanged(func(topics []string) {
		gotTopics[1] = topics
		changeCalled[1].Add(1)
	})
	mockPubSub3.SetOnSubscribedTopicsChanged(func(topics []string) {
		gotTopics[2] = topics
		changeCalled[2].Add(1)
	})

	require.NoError(t, subs.StartAppSubscriptions())
	assert.Equal(t, []string{"topic1"}, gotTopics[0])
	assert.Equal(t, []string{"topic2"}, gotTopics[1])
	assert.Equal(t, []string{"topic3"}, gotTopics[2])
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 1)

	subs.StopAppSubscriptions()
	assert.Eventually(t, func() bool {
		return changeCalled[0].Load() == 2 && changeCalled[1].Load() == 2 && changeCalled[2].Load() == 2
	}, time.Second, 10*time.Millisecond)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 1)

	require.NoError(t, subs.StartAppSubscriptions())
	assert.Equal(t, []string{"topic1"}, gotTopics[0])
	assert.Equal(t, []string{"topic2"}, gotTopics[1])
	assert.Equal(t, []string{"topic3"}, gotTopics[2])
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 2)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 2)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 2)

	subs.StopAppSubscriptions()
	assert.Eventually(t, func() bool {
		return changeCalled[0].Load() == 4 && changeCalled[1].Load() == 4 && changeCalled[2].Load() == 4
	}, time.Second, 10*time.Millisecond)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 2)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 2)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 2)

	require.NoError(t, subs.StartAppSubscriptions())
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 3)

	subs.StopAllSubscriptionsForever()
	assert.Eventually(t, func() bool {
		return changeCalled[0].Load() == 6 && changeCalled[1].Load() == 6 && changeCalled[2].Load() == 6
	}, time.Second, 10*time.Millisecond)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 3)

	require.NoError(t, subs.StartAppSubscriptions())
	require.NoError(t, subs.StartAppSubscriptions())
	require.NoError(t, subs.StartAppSubscriptions())
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 3)
}

func Test_initProgramaticSubscriptions(t *testing.T) {
	t.Run("get topic routes but no pubsubs are registered", func(t *testing.T) {
		compStore := compstore.New()
		subs := New(Options{
			CompStore:  compStore,
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			AppID:      TestRuntimeConfigID,
			Channels:   new(channels.Channels),
		})
		require.NoError(t, subs.initProgramaticSubscriptions(context.Background()))
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
	})

	t.Run("get topic routes but app channel is nil", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))
		subs := New(Options{
			CompStore:  compStore,
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			AppID:      TestRuntimeConfigID,
			Channels:   new(channels.Channels),
		})
		require.NoError(t, subs.initProgramaticSubscriptions(context.Background()))
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
	})

	t.Run("load programmatic subscriptions. Multiple calls invokes once", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))
		subs := New(Options{
			CompStore:  compStore,
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			AppID:      TestRuntimeConfigID,
			Channels:   new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		b, err := json.Marshal([]rtpubsub.SubscriptionJSON{
			{
				PubsubName: TestPubsubName,
				Topic:      "topic1",
				Routes: rtpubsub.RoutesJSON{
					Default: "/",
				},
			},
		})
		require.NoError(t, err)

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(b).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("context.backgroundCtx"), mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil)

		require.NoError(t, subs.initProgramaticSubscriptions(context.Background()))
		require.NoError(t, subs.initProgramaticSubscriptions(context.Background()))
		require.NoError(t, subs.initProgramaticSubscriptions(context.Background()))
		assert.Len(t, compStore.ListProgramaticSubscriptions(), 1)
	})
}

func TestReloadPubSub(t *testing.T) {
	mockPubSub1 := new(daprt.InMemoryPubsub)
	mockPubSub2 := new(daprt.InMemoryPubsub)
	mockPubSub3 := new(daprt.InMemoryPubsub)

	mockPubSub1.On("Init", mock.Anything).Return(nil)
	mockPubSub2.On("Init", mock.Anything).Return(nil)
	mockPubSub3.On("Init", mock.Anything).Return(nil)
	mockPubSub1.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub2.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub3.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).Return(nil)
	mockPubSub1.On("unsubscribed", "topic1").Return(nil)
	mockPubSub2.On("unsubscribed", "topic2").Return(nil)
	mockPubSub3.On("unsubscribed", "topic3").Return(nil)
	mockPubSub1.On("unsubscribed", "topic4").Return(nil)
	mockPubSub2.On("unsubscribed", "topic5").Return(nil)
	mockPubSub3.On("unsubscribed", "topic6").Return(nil)
	require.NoError(t, mockPubSub1.Init(context.Background(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub2.Init(context.Background(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub3.Init(context.Background(), contribpubsub.Metadata{}))

	compStore := compstore.New()
	compStore.AddPubSub("mockPubSub1", &rtpubsub.PubsubItem{
		Component: mockPubSub1,
	})
	compStore.AddPubSub("mockPubSub2", &rtpubsub.PubsubItem{
		Component: mockPubSub2,
	})
	compStore.AddPubSub("mockPubSub3", &rtpubsub.PubsubItem{
		Component: mockPubSub3,
	})

	compStore.SetProgramaticSubscriptions(
		rtpubsub.Subscription{
			PubsubName: "mockPubSub1",
			Topic:      "topic1",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		},
		rtpubsub.Subscription{
			PubsubName: "mockPubSub2",
			Topic:      "topic2",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		},
		rtpubsub.Subscription{
			PubsubName: "mockPubSub3",
			Topic:      "topic3",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		},
	)

	subs := New(Options{
		CompStore:  compStore,
		IsHTTP:     true,
		Resiliency: resiliency.New(logger.NewLogger("test")),
		Namespace:  "ns1",
		AppID:      TestRuntimeConfigID,
		Channels:   new(channels.Channels).WithAppChannel(new(channelt.MockAppChannel)),
	})
	subs.hasInitProg = true

	gotTopics := make([][]string, 3)
	changeCalled := make([]atomic.Int32, 3)
	mockPubSub1.SetOnSubscribedTopicsChanged(func(topics []string) {
		gotTopics[0] = append(gotTopics[0], topics...)
		slices.Sort(gotTopics[0])
		gotTopics[0] = slices.Compact(gotTopics[0])
		changeCalled[0].Add(1)
	})
	mockPubSub2.SetOnSubscribedTopicsChanged(func(topics []string) {
		gotTopics[1] = append(gotTopics[1], topics...)
		slices.Sort(gotTopics[1])
		gotTopics[1] = slices.Compact(gotTopics[1])
		changeCalled[1].Add(1)
	})
	mockPubSub3.SetOnSubscribedTopicsChanged(func(topics []string) {
		gotTopics[2] = append(gotTopics[2], topics...)
		slices.Sort(gotTopics[2])
		gotTopics[2] = slices.Compact(gotTopics[2])
		changeCalled[2].Add(1)
	})

	require.NoError(t, subs.StartAppSubscriptions())
	assert.Equal(t, []string{"topic1"}, gotTopics[0])
	assert.Equal(t, []string{"topic2"}, gotTopics[1])
	assert.Equal(t, []string{"topic3"}, gotTopics[2])
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 1)

	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub1||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub1",
			Topic:      "topic4",
			Routes:     subapi.Routes{Default: "/"},
		},
	})
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub2||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub2",
			Topic:      "topic5",
			Routes:     subapi.Routes{Default: "/"},
		},
	})
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub3||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub3",
			Topic:      "topic6",
			Routes:     subapi.Routes{Default: "/"},
		},
	})

	subs.ReloadPubSub("mockPubSub1")
	assert.Eventually(t, func() bool {
		return changeCalled[0].Load() == 4
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"topic1", "topic4"}, gotTopics[0])
	assert.Equal(t, []string{"topic2"}, gotTopics[1])
	assert.Equal(t, []string{"topic3"}, gotTopics[2])
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 0)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 0)

	subs.ReloadPubSub("mockPubSub2")
	assert.Eventually(t, func() bool {
		return changeCalled[1].Load() == 4
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"topic1", "topic4"}, gotTopics[0])
	assert.Equal(t, []string{"topic2", "topic5"}, gotTopics[1])
	assert.Equal(t, []string{"topic3"}, gotTopics[2])
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 1)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 0)

	subs.ReloadPubSub("mockPubSub3")
	assert.Eventually(t, func() bool {
		return changeCalled[2].Load() == 4
	}, time.Second, 10*time.Millisecond)
	assert.Equal(t, []string{"topic1", "topic4"}, gotTopics[0])
	assert.Equal(t, []string{"topic2", "topic5"}, gotTopics[1])
	assert.Equal(t, []string{"topic3", "topic6"}, gotTopics[2])
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 1)

	subs.StopPubSub("mockPubSub1")
	assert.Eventually(t, func() bool {
		return changeCalled[0].Load() == 6
	}, time.Second, 10*time.Millisecond)
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 1)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 1)

	subs.StopPubSub("mockPubSub2")
	assert.Eventually(t, func() bool {
		return changeCalled[1].Load() == 6
	}, time.Second, 10*time.Millisecond)
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 1)

	subs.StopPubSub("mockPubSub3")
	assert.Eventually(t, func() bool {
		return changeCalled[2].Load() == 6
	}, time.Second, 10*time.Millisecond)
	mockPubSub1.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub2.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub3.AssertNumberOfCalls(t, "Subscribe", 3)
	mockPubSub1.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub2.AssertNumberOfCalls(t, "unsubscribed", 3)
	mockPubSub3.AssertNumberOfCalls(t, "unsubscribed", 3)
}
