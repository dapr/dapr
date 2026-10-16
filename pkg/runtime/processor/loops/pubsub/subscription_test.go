/*
Copyright 2026 The Dapr Authors
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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	subapi "github.com/dapr/dapr/pkg/apis/subscriptions/v2alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/processor/loops"
	"github.com/dapr/dapr/pkg/runtime/processor/subscriber"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	daprt "github.com/dapr/dapr/pkg/testing"
	"github.com/dapr/kit/logger"
)

// The metadata API lists the declarative subscriptions in the compstore, and
// a client may publish the moment it sees one there, so a hot reloaded
// subscription must be subscribed on the component before it is listed.
func TestSubscriptionAddListsAfterSubscribe(t *testing.T) {
	compStore := compstore.New()

	var listedDuringSubscribe atomic.Bool
	ps := new(daprt.InMemoryPubsub)
	ps.On("Init", mock.Anything).Return(nil)
	ps.On("Subscribe", mock.AnythingOfType("pubsub.SubscribeRequest"), mock.AnythingOfType("pubsub.Handler")).
		Run(func(mock.Arguments) {
			_, listed := compStore.GetDeclarativeSubscription("sub1")
			listedDuringSubscribe.Store(listed)
		}).
		Return(nil)
	ps.On("unsubscribed", "topic1").Return(nil)
	require.NoError(t, ps.Init(t.Context(), contribpubsub.Metadata{}))
	compStore.AddPubSub("mypubsub", &rtpubsub.PubsubItem{Component: ps})

	subs := subscriber.New(subscriber.Options{
		CompStore:  compStore,
		IsHTTP:     true,
		Resiliency: resiliency.New(logger.NewLogger("test")),
		Namespace:  "ns1",
		AppID:      "app1",
		Channels:   new(channels.Channels).WithAppChannel(new(channelt.MockAppChannel)),
	})
	t.Cleanup(subs.StopAllSubscriptionsForever)
	require.NoError(t, subs.StartAppSubscriptions())

	c := New(Options{AppID: "app1", Subscriber: subs, CompStore: compStore})
	result := make(chan error, 1)
	c.handleSubscriptionAdd(&loops.SubscriptionAdd{
		Subscription: subapi.Subscription{
			ObjectMeta: metav1.ObjectMeta{Name: "sub1"},
			Spec: subapi.SubscriptionSpec{
				Pubsubname: "mypubsub",
				Topic:      "topic1",
				Routes:     subapi.Routes{Default: "/a"},
			},
		},
		Result: result,
	})
	require.NoError(t, <-result)

	ps.AssertNumberOfCalls(t, "Subscribe", 1)
	assert.False(t, listedDuringSubscribe.Load(), "the subscription was listed before its consumer subscribed")
	_, listed := compStore.GetDeclarativeSubscription("sub1")
	assert.True(t, listed)
}
