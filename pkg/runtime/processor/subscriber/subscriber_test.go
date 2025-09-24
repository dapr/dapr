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
	"encoding/json"
	"errors"
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
	require.NoError(t, mockPubSub1.Init(t.Context(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub2.Init(t.Context(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub3.Init(t.Context(), contribpubsub.Metadata{}))

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
	}, rtpubsub.ConnectionID(1))
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub2||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub2",
			Topic:      "topic5",
			Routes:     subapi.Routes{Default: "/"},
		},
	}, rtpubsub.ConnectionID(2))
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub3||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub3",
			Topic:      "topic6",
			Routes:     subapi.Routes{Default: "/"},
		},
	}, rtpubsub.ConnectionID(3))

	subs := New(Options{
		CompStore:                       compStore,
		IsHTTP:                          true,
		Resiliency:                      resiliency.New(logger.NewLogger("test")),
		Namespace:                       "ns1",
		AppID:                           TestRuntimeConfigID,
		Channels:                        new(channels.Channels).WithAppChannel(new(channelt.MockAppChannel)),
		ProgrammaticSubscriptionEnabled: true,
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

func Test_initProgrammaticSubscriptions(t *testing.T) {
	t.Run("get topic routes but no pubsubs are registered", func(t *testing.T) {
		compStore := compstore.New()
		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels),
			ProgrammaticSubscriptionEnabled: true,
		})
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
	})

	t.Run("get topic routes but app channel is nil", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))
		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels),
			ProgrammaticSubscriptionEnabled: true,
		})
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
	})

	t.Run("load programmatic subscriptions. Multiple calls invokes once", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))
		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels).WithAppChannel(mockAppChannel),
			ProgrammaticSubscriptionEnabled: true,
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

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil)

		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		assert.Len(t, compStore.ListProgramaticSubscriptions(), 1)
	})

	t.Run("skip programmatic subscription loading when disabled", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))
		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels).WithAppChannel(mockAppChannel),
			ProgrammaticSubscriptionEnabled: false, // Disabled
		})

		// Programmatic subscriptions should be skipped, so we don't expect any HTTP calls
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that no subscriptions were loaded
		assert.Empty(t, compStore.ListProgramaticSubscriptions())

		// Verify that hasInitProg is not set when programmatic subscriptions are disabled
		assert.False(t, subs.hasInitProg)

		// Verify that the mock app channel was never called
		mockAppChannel.AssertNotCalled(t, "InvokeMethod")
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
	require.NoError(t, mockPubSub1.Init(t.Context(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub2.Init(t.Context(), contribpubsub.Metadata{}))
	require.NoError(t, mockPubSub3.Init(t.Context(), contribpubsub.Metadata{}))

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
	}, rtpubsub.ConnectionID(1))
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub2||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub2",
			Topic:      "topic5",
			Routes:     subapi.Routes{Default: "/"},
		},
	}, rtpubsub.ConnectionID(2))
	compStore.AddStreamSubscription(&subapi.Subscription{
		ObjectMeta: metav1.ObjectMeta{Name: "sub3||"},
		Spec: subapi.SubscriptionSpec{
			Pubsubname: "mockPubSub3",
			Topic:      "topic6",
			Routes:     subapi.Routes{Default: "/"},
		},
	}, rtpubsub.ConnectionID(3))

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

func TestSubscriptionRetryMechanisms(t *testing.T) {
	createMockSetup := func() (*daprt.InMemoryPubsub, *compstore.ComponentStore) {
		mockPubSub := new(daprt.InMemoryPubsub)
		mockPubSub.On("Init", mock.Anything).Return(nil)
		mockPubSub.On("unsubscribed", "topic1").Return(nil)
		require.NoError(t, mockPubSub.Init(t.Context(), contribpubsub.Metadata{}))

		compStore := compstore.New()
		compStore.AddPubSub("mockPubSub", &rtpubsub.PubsubItem{
			Component: mockPubSub,
		})

		return mockPubSub, compStore
	}

	t.Run("StartAppSubscriptions succeeds after retries", func(t *testing.T) {
		t.Parallel()

		mockPubSub, compStore := createMockSetup()

		var calls atomic.Int32
		subsCall := mockPubSub.
			On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).
			Return(errors.New("temporary subscription failure"))
		subsCall.Run(func(args mock.Arguments) {
			if calls.Add(1) == 3 {
				// Succeed on the 3rd attempt
				subsCall.Return(nil)
			}
		})

		compStore.SetProgramaticSubscriptions(
			rtpubsub.Subscription{
				PubsubName: "mockPubSub",
				Topic:      "topic1",
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

		err := subs.StartAppSubscriptions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "temporary subscription failure")

		assert.Eventually(t, func() bool {
			subs.lock.RLock()
			hasSubscription := len(subs.appSubs["mockPubSub"]) == 1
			subs.lock.RUnlock()
			return hasSubscription
		}, 5*time.Second, 100*time.Millisecond)

		assert.Equal(t, 3, int(calls.Load()))
		assert.True(t, subs.appSubActive)
	})

	t.Run("ReloadDeclaredAppSubscription succeeds after retries", func(t *testing.T) {
		t.Parallel()

		mockPubSub, compStore := createMockSetup()

		var calls atomic.Int32
		subsCall := mockPubSub.
			On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).
			Return(errors.New("temporary failure"))
		subsCall.Run(func(args mock.Arguments) {
			if calls.Add(1) == 2 {
				// Succeed on the 2nd attempt
				subsCall.Return(nil)
			}
		})

		compStore.AddDeclarativeSubscription(&subapi.Subscription{
			ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
			Spec: subapi.SubscriptionSpec{
				Pubsubname: "mockPubSub",
				Topic:      "topic1",
				Routes:     subapi.Routes{Default: "/"},
			},
		}, rtpubsub.Subscription{
			PubsubName: "mockPubSub",
			Topic:      "topic1",
			Rules:      []*rtpubsub.Rule{{Path: "/"}},
		})

		subs := New(Options{
			CompStore:  compStore,
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			AppID:      TestRuntimeConfigID,
			Channels:   new(channels.Channels).WithAppChannel(new(channelt.MockAppChannel)),
		})
		subs.appSubActive = true

		err := subs.ReloadDeclaredAppSubscription("sub1", "mockPubSub")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "temporary failure")

		assert.Eventually(t, func() bool {
			subs.lock.RLock()
			hasSubscription := len(subs.appSubs["mockPubSub"]) == 1
			subs.lock.RUnlock()
			return hasSubscription
		}, 5*time.Second, 100*time.Millisecond)

		assert.Equal(t, 2, int(calls.Load()))
	})

	t.Run("retry stops when subscriber is closed", func(t *testing.T) {
		t.Parallel()

		mockPubSub, compStore := createMockSetup()

		var callCount atomic.Int32
		mockPubSub.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).
			Run(func(args mock.Arguments) {
				callCount.Add(1)
			}).
			Return(errors.New("persistent subscription failure"))

		compStore.SetProgramaticSubscriptions(
			rtpubsub.Subscription{
				PubsubName: "mockPubSub",
				Topic:      "topic1",
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

		err := subs.StartAppSubscriptions()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "persistent subscription failure")

		initialCallCount := callCount.Load()

		time.Sleep(500 * time.Millisecond)
		subs.StopAllSubscriptionsForever()

		time.Sleep(500 * time.Millisecond)
		finalCallCount := callCount.Load()

		assert.Greater(t, finalCallCount, initialCallCount)

		time.Sleep(1 * time.Second)
		verifyCallCount := callCount.Load()
		assert.Equal(t, finalCallCount, verifyCallCount, "Retries should have stopped after subscriber closure")
	})
}

func TestProgrammaticSubscriptionEnabled(t *testing.T) {
	t.Run("programmatic subscription enabled - should load subscriptions", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))

		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels).WithAppChannel(mockAppChannel),
			ProgrammaticSubscriptionEnabled: true, // Enabled
		})

		// Setup mock response for subscription endpoint call
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

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil)

		// Call initProgrammaticSubscriptions
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that programmatic subscriptions were loaded
		assert.Len(t, compStore.ListProgramaticSubscriptions(), 1)
		assert.True(t, subs.hasInitProg, "hasInitProg should be set when programmatic subscriptions are enabled")

		// Verify that the mock app channel was called
		mockAppChannel.AssertCalled(t, "InvokeMethod", mock.Anything, mock.Anything)
	})

	t.Run("programmatic subscription disabled - should skip loading", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))

		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels).WithAppChannel(mockAppChannel),
			ProgrammaticSubscriptionEnabled: false, // Disabled
		})

		// Call initProgrammaticSubscriptions - should return early without making HTTP calls
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that no subscriptions were loaded
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
		assert.False(t, subs.hasInitProg, "hasInitProg should not be set when programmatic subscriptions are disabled")

		// Verify that the mock app channel was never called
		mockAppChannel.AssertNotCalled(t, "InvokeMethod")
	})

	t.Run("default programmatic subscription behavior (false by default)", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))

		// Don't set ProgrammaticSubscriptionEnabled - should default to false
		subs := New(Options{
			CompStore:  compStore,
			IsHTTP:     true,
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Namespace:  "ns1",
			AppID:      TestRuntimeConfigID,
			Channels:   new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// Call initProgrammaticSubscriptions - should return early without making HTTP calls
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that no subscriptions were loaded (default behavior)
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
		assert.False(t, subs.hasInitProg, "hasInitProg should not be set when programmatic subscriptions are disabled by default")

		// Verify that the mock app channel was never called
		mockAppChannel.AssertNotCalled(t, "InvokeMethod")
	})

	t.Run("programmatic subscription enabled - no pubsubs registered", func(t *testing.T) {
		compStore := compstore.New()
		// Don't add any pubsubs

		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels),
			ProgrammaticSubscriptionEnabled: true,
		})

		// Call initProgrammaticSubscriptions - should return early because no pubsubs are registered
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that no subscriptions were loaded and hasInitProg is not set
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
		assert.False(t, subs.hasInitProg, "hasInitProg should not be set when no pubsubs are registered")
	})

	t.Run("programmatic subscription enabled - app channel is nil", func(t *testing.T) {
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))

		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels), // No app channel set
			ProgrammaticSubscriptionEnabled: true,
		})

		// Call initProgrammaticSubscriptions - should return early because app channel is nil
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that no subscriptions were loaded and hasInitProg is not set
		assert.Empty(t, compStore.ListProgramaticSubscriptions())
		assert.False(t, subs.hasInitProg, "hasInitProg should not be set when app channel is nil")
	})

	t.Run("multiple calls to initProgrammaticSubscriptions when enabled", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		compStore := compstore.New()
		compStore.AddPubSub(TestPubsubName, new(rtpubsub.PubsubItem))

		subs := New(Options{
			CompStore:                       compStore,
			IsHTTP:                          true,
			Resiliency:                      resiliency.New(logger.NewLogger("test")),
			Namespace:                       "ns1",
			AppID:                           TestRuntimeConfigID,
			Channels:                        new(channels.Channels).WithAppChannel(mockAppChannel),
			ProgrammaticSubscriptionEnabled: true,
		})

		// Setup mock response
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

		mockAppChannel.On("InvokeMethod", mock.AnythingOfType("*context.cancelCtx"), mock.AnythingOfType("*v1.InvokeMethodRequest")).Return(fakeResp, nil)

		// Call initProgrammaticSubscriptions multiple times
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))
		require.NoError(t, subs.initProgrammaticSubscriptions(t.Context()))

		// Verify that subscriptions were loaded only once
		assert.Len(t, compStore.ListProgramaticSubscriptions(), 1)
		assert.True(t, subs.hasInitProg)

		// Verify that the mock app channel was called only once (due to hasInitProg flag)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}
