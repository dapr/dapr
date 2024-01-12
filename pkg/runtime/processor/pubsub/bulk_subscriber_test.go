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
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	componentsV1alpha1 "github.com/dapr/dapr/pkg/apis/components/v1alpha1"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	"github.com/dapr/dapr/pkg/runtime/meta"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	daprt "github.com/dapr/dapr/pkg/testing"
	testinggrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
)

const (
	TestRuntimeConfigID = "consumer0"

	eventKey = `"event":`

	data1     string = `{"orderId":"1"}`
	data2     string = `{"orderId":"2"}`
	data3     string = `{"orderId":"3"}`
	data4     string = `{"orderId":"4"}`
	data5     string = `{"orderId":"5"}`
	data6     string = `{"orderId":"6"}`
	data7     string = `{"orderId":"7"}`
	data8     string = `{"orderId":"8"}`
	data9     string = ``
	data10    string = `{"orderId":"10"}`
	ext1Key   string = "ext1Key"
	ext1Value string = "ext1Value"
	ext2Key   string = "ext2Key"
	ext2Value string = "ext2Value"
	//nolint:goconst
	order1     string = `{"data":` + data1 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"9b6767c3-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	order2     string = `{"data":` + data2 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"993f4e4a-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type2"}`
	order3     string = `{"data":` + data3 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"6767010u-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	order4     string = `{"data":` + data4 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"91011121-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type2"}`
	order5     string = `{"data":` + data5 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"718271cd-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	order6     string = `{"data":` + data6 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"7uw2233d-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type2"}`
	order7     string = `{"data":` + data7 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"78sqs98s-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"type1"}`
	order8     string = `{"data":` + data8 + `,"datacontenttype":"application/json","` + ext1Key + `":"` + ext1Value + `","id":"45122j82-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type1"}`
	order9     string = `{"` + ext1Key + `":"` + ext1Value + `","orderId":"9","type":"type1"}`
	order10    string = `{"data":` + data10 + `,"datacontenttype":"application/json","` + ext2Key + `":"` + ext2Value + `","id":"ded2rd44-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type2"}`
	wrongOrder string = `{"data":` + data2 + `,"datacontenttype":"application/xml;wwwwwww","` + ext2Key + `":"` + ext2Value + `","id":"993f4e4a-05e5-4772-94a4-e899b1af0131","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","traceparent":"00-1343b02c3af4f9b352d4cb83d6c8cb81-82a64f8c4433e2c4-01","tracestate":"","type":"type2"}`
	orders1    string = "orders1"
)

func getBulkMessageEntries(len int) []contribpubsub.BulkMessageEntry {
	bulkEntries := make([]contribpubsub.BulkMessageEntry, 10)

	bulkEntries[0] = contribpubsub.BulkMessageEntry{EntryId: "1111111a", Event: []byte(order1)}
	bulkEntries[1] = contribpubsub.BulkMessageEntry{EntryId: "2222222b", Event: []byte(order2)}
	bulkEntries[2] = contribpubsub.BulkMessageEntry{EntryId: "333333c", Event: []byte(order3)}
	bulkEntries[3] = contribpubsub.BulkMessageEntry{EntryId: "4444444d", Event: []byte(order4)}
	bulkEntries[4] = contribpubsub.BulkMessageEntry{EntryId: "5555555e", Event: []byte(order5)}
	bulkEntries[5] = contribpubsub.BulkMessageEntry{EntryId: "66666666f", Event: []byte(order6)}
	bulkEntries[6] = contribpubsub.BulkMessageEntry{EntryId: "7777777g", Event: []byte(order7)}
	bulkEntries[7] = contribpubsub.BulkMessageEntry{EntryId: "8888888h", Event: []byte(order8)}
	bulkEntries[8] = contribpubsub.BulkMessageEntry{EntryId: "9999999i", Event: []byte(order9)}
	bulkEntries[9] = contribpubsub.BulkMessageEntry{EntryId: "10101010j", Event: []byte(order10)}

	return bulkEntries[:len]
}

func getBulkMessageEntriesWithWrongData() []contribpubsub.BulkMessageEntry {
	bulkEntries := make([]contribpubsub.BulkMessageEntry, 1)
	bulkEntries[0] = contribpubsub.BulkMessageEntry{EntryId: "1", Event: []byte(wrongOrder)}
	return bulkEntries
}

type ExpectedExtension struct {
	extKey   string
	extValue string
}

func getExpectedBulkRequests() map[string][]string {
	mapPathEntries := map[string][]string{
		"type1": {data1, data3, data5, data7, data8, data9},
		"type2": {data2, data4, data6, data10},
	}
	return mapPathEntries
}

func getExpectedExtension() map[string]ExpectedExtension {
	return map[string]ExpectedExtension{
		"type1": {ext1Key, ext1Value},
		"type2": {ext2Key, ext2Value},
	}
}

func TestBulkSubscribe(t *testing.T) {
	testBulkSubscribePubsub := "bulkSubscribePubSub"
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: testBulkSubscribePubsub,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}

	t.Run("bulk Subscribe Message for raw payload", func(t *testing.T) {
		ps := New(Options{
			Registry:       registry.New(registry.NewOptions()).PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName: testBulkSubscribePubsub, Topic: "topic0", Route: "orders",
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
				Metadata:      map[string]string{"rawPayload": "true"},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		err := ps.Publish(context.TODO(), &contribpubsub.PublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Data:       []byte(`{"orderId":"1"}`),
		})
		require.Error(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Contains(t, string(reqs["orders"]), `event":"eyJvcmRlcklkIjoiMSJ9"`)
	})

	t.Run("bulk Subscribe Message for cloud event", func(t *testing.T) {
		ps := New(Options{
			Registry:       registry.New(registry.NewOptions()).PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{}
			},
			"mockPubSub",
		)
		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName: testBulkSubscribePubsub, Topic: "topic0", Route: "orders",
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		order := `{"data":{"orderId":1},"datacontenttype":"application/json","id":"8b540b03-04b5-4871-96ae-c6bde0d5e16d","pubsubname":"orderpubsub","source":"checkout","specversion":"1.0","topic":"orders","traceid":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","traceparent":"00-e61de949bb4de415a7af49fc86675648-ffb64972bb907224-01","tracestate":"","type":"com.dapr.event.sent"}`

		err := ps.Publish(context.TODO(), &contribpubsub.PublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Data:       []byte(order),
		})
		require.Error(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Contains(t, string(reqs["orders"]), eventKey+order)
	})

	t.Run("bulk Subscribe multiple Messages at once for cloud events", func(t *testing.T) {
		ps := New(Options{
			Registry:       registry.New(registry.NewOptions()).PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ms := &mockSubscribePubSub{
			features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
		}
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return ms
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName: testBulkSubscribePubsub, Topic: "topic0", Route: "orders",
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)
		fakeResp1 := invokev1.NewInvokeMethodResponse(200, "OK", nil)
		defer fakeResp1.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp1, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		msgArr := getBulkMessageEntries(2)

		ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})

		assert.Len(t, ms.GetBulkResponse().Statuses, 2)
		require.Error(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a", "2222222b"))

		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.Contains(t, string(reqs["orders"]), eventKey+order1)
		assert.Contains(t, string(reqs["orders"]), eventKey+order2)

		fakeResp2 := invokev1.NewInvokeMethodResponse(404, "OK", nil)
		defer fakeResp2.Close()
		mockAppChannel1 := new(channelt.MockAppChannel)
		mockAppChannel1.Init()
		ps.channels.WithAppChannel(mockAppChannel1)
		mockAppChannel1.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp2, nil)

		msgArr = getBulkMessageEntries(3)

		ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})

		assert.Len(t, ms.GetBulkResponse().Statuses, 3)
		require.NoError(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a", "2222222b", "333333c"))

		assert.Equal(t, 2, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs = mockAppChannel1.GetInvokedRequest()
		mockAppChannel1.AssertNumberOfCalls(t, "InvokeMethod", 1)
		assert.Contains(t, string(reqs["orders"]), eventKey+order1)
		assert.Contains(t, string(reqs["orders"]), eventKey+order2)
		assert.Contains(t, string(reqs["orders"]), eventKey+order3)

		fakeResp3 := invokev1.NewInvokeMethodResponse(400, "OK", nil)
		defer fakeResp3.Close()
		mockAppChannel2 := new(channelt.MockAppChannel)
		mockAppChannel2.Init()
		ps.channels.WithAppChannel(mockAppChannel2)
		mockAppChannel2.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp3, nil)

		msgArr = getBulkMessageEntries(4)

		ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})

		assert.Len(t, ms.GetBulkResponse().Statuses, 4)
		require.Error(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a", "2222222b", "333333c", "4444444d"))

		assert.Equal(t, 3, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs = mockAppChannel2.GetInvokedRequest()
		mockAppChannel2.AssertNumberOfCalls(t, "InvokeMethod", 1)
		assert.Contains(t, string(reqs["orders"]), eventKey+order1)
		assert.Contains(t, string(reqs["orders"]), eventKey+order2)
		assert.Contains(t, string(reqs["orders"]), eventKey+order3)
		assert.Contains(t, string(reqs["orders"]), eventKey+order4)

		mockAppChannel3 := new(channelt.MockAppChannel)
		mockAppChannel3.Init()
		ps.channels.WithAppChannel(mockAppChannel3)
		mockAppChannel3.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(nil, errors.New("Mock error"))
		msgArr = getBulkMessageEntries(1)

		ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})

		assert.Len(t, ms.GetBulkResponse().Statuses, 1)
		require.Error(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a"))

		assert.Equal(t, 4, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs = mockAppChannel3.GetInvokedRequest()
		mockAppChannel3.AssertNumberOfCalls(t, "InvokeMethod", 1)
		assert.Contains(t, string(reqs["orders"]), eventKey+order1)
	})

	t.Run("bulk Subscribe events on different paths", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName: testBulkSubscribePubsub,
				Topic:      "topic0",
				Routes: runtimePubsub.RoutesJSON{
					Rules: []*runtimePubsub.RuleJSON{
						{
							Path:  orders1,
							Match: `event.type == "type1"`,
						},
						{
							Path:  "orders2",
							Match: `event.type == "type2"`,
						},
					},
				},
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		msgArr := getBulkMessageEntries(2)

		_, err := ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)

		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.Contains(t, string(reqs["orders1"]), eventKey+order1)
		assert.NotContains(t, string(reqs["orders1"]), eventKey+order2)
		assert.Contains(t, string(reqs["orders2"]), eventKey+order2)
		assert.NotContains(t, string(reqs["orders2"]), eventKey+order1)
	})

	t.Run("verify Responses when bulk Subscribe events on different paths", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName: testBulkSubscribePubsub,
				Topic:      "topic0",
				Routes: runtimePubsub.RoutesJSON{
					Rules: []*runtimePubsub.RuleJSON{
						{
							Path:  orders1,
							Match: `event.type == "type1"`,
						},
						{
							Path:  "orders2",
							Match: `event.type == "type2"`,
						},
					},
				},
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		msgArr := getBulkMessageEntries(10)
		responseItemsOrders1 := contribpubsub.AppBulkResponse{
			AppResponses: []contribpubsub.AppBulkResponseEntry{
				{EntryId: "1111111a", Status: "SUCCESS"},
				{EntryId: "333333c", Status: "RETRY"},
				{EntryId: "5555555e", Status: "DROP"},
				{EntryId: "7777777g", Status: "RETRY"},
				{EntryId: "8888888h", Status: "SUCCESS"},
				{EntryId: "9999999i", Status: "SUCCESS"},
			},
		}

		resp1, _ := json.Marshal(responseItemsOrders1)
		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(resp1).
			WithContentType("application/json")
		defer respInvoke1.Close()

		responseItemsOrders2 := contribpubsub.AppBulkResponse{
			AppResponses: []contribpubsub.AppBulkResponseEntry{
				{EntryId: "2222222b", Status: "SUCCESS"},
				{EntryId: "4444444d", Status: "DROP"},
				{EntryId: "66666666f", Status: "DROP"},
				{EntryId: "10101010j", Status: "SUCCESS"},
			},
		}

		resp2, _ := json.Marshal(responseItemsOrders2)
		respInvoke2 := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(resp2).
			WithContentType("application/json")
		defer respInvoke2.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("orders1")).Return(respInvoke1, nil)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("orders2")).Return(respInvoke2, nil)

		_, err := ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)

		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
		assert.True(t, verifyIfEventContainsStrings(reqs["orders1"], eventKey+order1,
			eventKey+order3, eventKey+order5, eventKey+order7, eventKey+order8, eventKey+order9))
		assert.True(t, verifyIfEventNotContainsStrings(reqs["orders1"], eventKey+order2,
			eventKey+order4, eventKey+order6, eventKey+order10))
		assert.True(t, verifyIfEventContainsStrings(reqs["orders2"], eventKey+order2,
			eventKey+order4, eventKey+order6, eventKey+order10))
		assert.True(t, verifyIfEventNotContainsStrings(reqs["orders2"], eventKey+order1,
			eventKey+order3, eventKey+order5, eventKey+order7, eventKey+order8, eventKey+order9))

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: false},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("verify Responses when entryId supplied blank while sending messages", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName:    testBulkSubscribePubsub,
				Topic:         "topic0",
				Route:         "orders",
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		msgArr := getBulkMessageEntries(4)
		msgArr[0].EntryId = ""
		msgArr[2].EntryId = ""

		responseItemsOrders1 := contribpubsub.AppBulkResponse{
			AppResponses: []contribpubsub.AppBulkResponseEntry{
				{EntryId: "2222222b", Status: "SUCCESS"},
				{EntryId: "4444444d", Status: "SUCCESS"},
			},
		}

		resp1, _ := json.Marshal(responseItemsOrders1)
		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(resp1).
			WithContentType("application/json")
		defer respInvoke1.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("orders")).Return(respInvoke1, nil)

		_, err := ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)

		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.True(t, verifyIfEventContainsStrings(reqs["orders"], eventKey+order2,
			eventKey+order4))
		assert.True(t, verifyIfEventNotContainsStrings(reqs["orders"], eventKey+order1,
			eventKey+order3))

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "", IsError: true},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "", IsError: true},
				{EntryId: "4444444d", IsError: false},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("verify bulk Subscribe Responses when App sends back out of order entryIds", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName:    testBulkSubscribePubsub,
				Topic:         "topic0",
				Route:         "orders",
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		msgArr := getBulkMessageEntries(5)

		responseItemsOrders1 := contribpubsub.AppBulkResponse{
			AppResponses: []contribpubsub.AppBulkResponseEntry{
				{EntryId: "2222222b", Status: "RETRY"},
				{EntryId: "333333c", Status: "SUCCESS"},
				{EntryId: "5555555e", Status: "RETRY"},
				{EntryId: "1111111a", Status: "SUCCESS"},
				{EntryId: "4444444d", Status: "SUCCESS"},
			},
		}

		resp1, _ := json.Marshal(responseItemsOrders1)
		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(resp1).
			WithContentType("application/json")
		defer respInvoke1.Close()

		mockAppChannel.On(
			"InvokeMethod",
			mock.MatchedBy(matchContextInterface),
			matchDaprRequestMethod("orders"),
		).Return(respInvoke1, nil)

		_, err := ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)

		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.True(t, verifyIfEventContainsStrings(reqs["orders"], eventKey+order1,
			eventKey+order2, eventKey+order3, eventKey+order4, eventKey+order5))

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: true},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("verify bulk Subscribe Responses when App sends back wrong entryIds", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{
				PubsubName:    testBulkSubscribePubsub,
				Topic:         "topic0",
				Route:         "orders",
				BulkSubscribe: runtimePubsub.BulkSubscribeJSON{Enabled: true},
			},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).Return(fakeResp, nil)

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		msgArr := getBulkMessageEntries(5)

		responseItemsOrders1 := contribpubsub.AppBulkResponse{
			AppResponses: []contribpubsub.AppBulkResponseEntry{
				{EntryId: "wrongEntryId1", Status: "SUCCESS"},
				{EntryId: "2222222b", Status: "RETRY"},
				{EntryId: "333333c", Status: "SUCCESS"},
				{EntryId: "wrongEntryId2", Status: "SUCCESS"},
				{EntryId: "5555555e", Status: "RETRY"},
			},
		}

		resp1, _ := json.Marshal(responseItemsOrders1)
		respInvoke1 := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(resp1).
			WithContentType("application/json")
		defer respInvoke1.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface),
			matchDaprRequestMethod("orders"),
		).Return(respInvoke1, nil)

		_, err := ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)

		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.bulkPubCount["topic0"])
		assert.True(t, pubsubIns.isBulkSubscribe)
		reqs := mockAppChannel.GetInvokedRequest()
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 2)
		assert.True(t, verifyIfEventContainsStrings(reqs["orders"], eventKey+order1,
			eventKey+order2, eventKey+order3, eventKey+order4, eventKey+order5))

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})
}

func TestBulkSubscribeGRPC(t *testing.T) {
	testBulkSubscribePubsub := "bulkSubscribePubSub"
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: testBulkSubscribePubsub,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}

	t.Run("GRPC - bulk Subscribe Message for raw payload", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         false,
			Channels:       new(channels.Channels),
		})
		ms := &mockSubscribePubSub{
			features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
		}
		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return ms
			},
			"mockPubSub",
		)

		subscriptionItems := runtimev1pb.ListTopicSubscriptionsResponse{
			Subscriptions: []*runtimev1pb.TopicSubscription{
				{
					PubsubName: testBulkSubscribePubsub,
					Topic:      "topic0",
					Routes: &runtimev1pb.TopicRoutes{
						Default: "orders",
					},
					BulkSubscribe: &runtimev1pb.BulkSubscribeConfig{Enabled: true},
					Metadata:      map[string]string{"rawPayload": "true"},
				},
			},
		}

		nbei1 := contribpubsub.BulkMessageEntry{EntryId: "1111111a", Event: []byte(`{"orderId":"1"}`)}
		nbei2 := contribpubsub.BulkMessageEntry{EntryId: "2222222b", Event: []byte(`{"orderId":"2"}`)}
		msgArr := []contribpubsub.BulkMessageEntry{nbei1, nbei2}
		responseEntries := make([]*runtimev1pb.TopicEventBulkResponseEntry, 2)
		for k, msg := range msgArr {
			responseEntries[k] = &runtimev1pb.TopicEventBulkResponseEntry{
				EntryId: msg.EntryId,
			}
		}
		responseEntries = setBulkResponseStatus(responseEntries,
			runtimev1pb.TopicEventResponse_DROP,
			runtimev1pb.TopicEventResponse_SUCCESS)

		responses := runtimev1pb.TopicEventBulkResponse{
			Statuses: responseEntries,
		}
		mapResp := make(map[string]*runtimev1pb.TopicEventBulkResponse)
		mapResp["orders"] = &responses
		// create mock application server first
		mockServer := &channelt.MockServer{
			ListTopicSubscriptionsResponse: &subscriptionItems,
			BulkResponsePerPath:            mapResp,
			Error:                          nil,
		}
		grpcServer := startTestAppCallbackAlphaGRPCServer(t, port, mockServer)
		if grpcServer != nil {
			// properly stop the gRPC server
			defer grpcServer.Stop()
		}

		grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})

		// create a new AppChannel and gRPC client for every test
		ps.channels = channels.New(channels.Options{
			ComponentStore: compstore.New(),
			Registry:       reg,
			GlobalConfig:   new(config.Configuration),
			GRPC:           grpc,
			AppConnectionConfig: config.AppConnectionConfig{
				Port: port,
			},
		})
		require.NoError(t, ps.channels.Refresh())
		ps.grpc = grpc

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		_, err = ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		assert.Len(t, ms.GetBulkResponse().Statuses, 2)
		require.NoError(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a", "2222222b"))

		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: false},
			},
		}
		assert.Contains(t, string(mockServer.RequestsReceived["orders"].GetEntries()[0].GetBytes()), `{"orderId":"1"}`)
		assert.Contains(t, string(mockServer.RequestsReceived["orders"].GetEntries()[1].GetBytes()), `{"orderId":"2"}`)
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))

		mockServer.BulkResponsePerPath = nil
		mockServer.Error = status.Error(codes.Unimplemented, "method not implemented")
		ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		assert.Len(t, ms.GetBulkResponse().Statuses, 2)
		require.NoError(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a", "2222222b"))

		mockServer.Error = status.Error(codes.Unknown, "unknown error")
		ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		assert.Len(t, ms.GetBulkResponse().Statuses, 2)
		require.Error(t, ms.GetBulkResponse().Error)
		require.NoError(t, assertItemExistsOnce(ms.GetBulkResponse().Statuses, "1111111a", "2222222b"))
	})

	t.Run("GRPC - bulk Subscribe cloud event Message on different paths and verify response", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         false,
			Channels:       new(channels.Channels),
		})

		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := runtimev1pb.ListTopicSubscriptionsResponse{
			Subscriptions: []*runtimev1pb.TopicSubscription{
				{
					PubsubName: testBulkSubscribePubsub,
					Topic:      "topic0",
					Routes: &runtimev1pb.TopicRoutes{
						Rules: []*runtimev1pb.TopicRule{
							{
								Path:  orders1,
								Match: `event.type == "type1"`,
							},
							{
								Path:  "orders2",
								Match: `event.type == "type2"`,
							},
						},
					},
					BulkSubscribe: &runtimev1pb.BulkSubscribeConfig{Enabled: true},
				},
			},
		}
		msgArr := getBulkMessageEntries(10)
		responseEntries1 := make([]*runtimev1pb.TopicEventBulkResponseEntry, 6)
		responseEntries2 := make([]*runtimev1pb.TopicEventBulkResponseEntry, 4)
		i := 0
		j := 0
		for k, msg := range msgArr {
			if strings.Contains(string(msgArr[k].Event), "type1") {
				responseEntries1[i] = &runtimev1pb.TopicEventBulkResponseEntry{
					EntryId: msg.EntryId,
				}
				i++
			} else if strings.Contains(string(msgArr[k].Event), "type2") {
				responseEntries2[j] = &runtimev1pb.TopicEventBulkResponseEntry{
					EntryId: msg.EntryId,
				}
				j++
			}
		}
		responseEntries1 = setBulkResponseStatus(responseEntries1,
			runtimev1pb.TopicEventResponse_DROP,
			runtimev1pb.TopicEventResponse_RETRY,
			runtimev1pb.TopicEventResponse_DROP,
			runtimev1pb.TopicEventResponse_RETRY,
			runtimev1pb.TopicEventResponse_SUCCESS,
			runtimev1pb.TopicEventResponse_DROP)

		responseEntries2 = setBulkResponseStatus(responseEntries2,
			runtimev1pb.TopicEventResponse_RETRY,
			runtimev1pb.TopicEventResponse_DROP,
			runtimev1pb.TopicEventResponse_RETRY,
			runtimev1pb.TopicEventResponse_SUCCESS)

		responses1 := runtimev1pb.TopicEventBulkResponse{
			Statuses: responseEntries1,
		}
		responses2 := runtimev1pb.TopicEventBulkResponse{
			Statuses: responseEntries2,
		}
		mapResp := make(map[string]*runtimev1pb.TopicEventBulkResponse)
		mapResp[orders1] = &responses1
		mapResp["orders2"] = &responses2
		// create mock application server first
		mockServer := &channelt.MockServer{
			ListTopicSubscriptionsResponse: &subscriptionItems,
			BulkResponsePerPath:            mapResp,
			Error:                          nil,
		}
		grpcServer := startTestAppCallbackAlphaGRPCServer(t, port, mockServer)
		if grpcServer != nil {
			// properly stop the gRPC server
			defer grpcServer.Stop()
		}

		// create a new AppChannel and gRPC client for every test
		grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
		ps.channels = channels.New(channels.Options{
			ComponentStore:      compstore.New(),
			Registry:            reg,
			GlobalConfig:        new(config.Configuration),
			AppConnectionConfig: config.AppConnectionConfig{Port: port},
			GRPC:                grpc,
		})
		require.NoError(t, ps.channels.Refresh())
		ps.grpc = grpc

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		_, err = ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: true},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: false},
				{EntryId: "66666666f", IsError: true},
				{EntryId: "7777777g", IsError: true},
				{EntryId: "8888888h", IsError: false},
				{EntryId: "9999999i", IsError: false},
				{EntryId: "10101010j", IsError: false},
			},
		}
		assert.True(t, verifyBulkSubscribeRequest(getExpectedBulkRequests()["type1"],
			getExpectedExtension()["type1"], mockServer.RequestsReceived[orders1]))
		assert.True(t, verifyBulkSubscribeRequest(getExpectedBulkRequests()["type2"],
			getExpectedExtension()["type2"], mockServer.RequestsReceived["orders2"]))
		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("GRPC - verify Responses when entryId supplied blank while sending messages", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         false,
			Channels:       new(channels.Channels),
		})

		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := runtimev1pb.ListTopicSubscriptionsResponse{
			Subscriptions: []*runtimev1pb.TopicSubscription{
				{
					PubsubName: testBulkSubscribePubsub,
					Topic:      "topic0",
					Routes: &runtimev1pb.TopicRoutes{
						Default: "orders",
					},
					BulkSubscribe: &runtimev1pb.BulkSubscribeConfig{Enabled: true},
				},
			},
		}
		msgArr := getBulkMessageEntries(4)
		msgArr[0].EntryId = ""
		msgArr[2].EntryId = ""
		responseEntries := make([]*runtimev1pb.TopicEventBulkResponseEntry, 4)
		for k, msg := range msgArr {
			responseEntries[k] = &runtimev1pb.TopicEventBulkResponseEntry{
				EntryId: msg.EntryId,
			}
		}
		responseEntries[1].Status = runtimev1pb.TopicEventResponse_SUCCESS
		responseEntries[3].Status = runtimev1pb.TopicEventResponse_SUCCESS
		responses := runtimev1pb.TopicEventBulkResponse{
			Statuses: responseEntries,
		}
		mapResp := make(map[string]*runtimev1pb.TopicEventBulkResponse)
		mapResp["orders"] = &responses
		// create mock application server first
		mockServer := &channelt.MockServer{
			ListTopicSubscriptionsResponse: &subscriptionItems,
			BulkResponsePerPath:            mapResp,
			Error:                          nil,
		}
		grpcServer := startTestAppCallbackAlphaGRPCServer(t, port, mockServer)
		if grpcServer != nil {
			// properly stop the gRPC server
			defer grpcServer.Stop()
		}

		grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
		ps.channels = channels.New(channels.Options{
			ComponentStore:      compstore.New(),
			Registry:            reg,
			GlobalConfig:        new(config.Configuration),
			GRPC:                grpc,
			AppConnectionConfig: config.AppConnectionConfig{Port: port},
		})
		require.NoError(t, err)
		require.NoError(t, ps.channels.Refresh())
		ps.grpc = grpc

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		_, err = ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "", IsError: true},
				{EntryId: "2222222b", IsError: false},
				{EntryId: "", IsError: true},
				{EntryId: "4444444d", IsError: false},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("GRPC - verify bulk Subscribe Responses when App sends back out of order entryIds", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         false,
			Channels:       new(channels.Channels),
		})

		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := runtimev1pb.ListTopicSubscriptionsResponse{
			Subscriptions: []*runtimev1pb.TopicSubscription{
				{
					PubsubName: testBulkSubscribePubsub,
					Topic:      "topic0",
					Routes: &runtimev1pb.TopicRoutes{
						Default: "orders",
					},
					BulkSubscribe: &runtimev1pb.BulkSubscribeConfig{Enabled: true},
				},
			},
		}
		msgArr := getBulkMessageEntries(5)
		responseEntries := make([]*runtimev1pb.TopicEventBulkResponseEntry, 5)
		responseEntries[0] = &runtimev1pb.TopicEventBulkResponseEntry{
			EntryId: msgArr[1].EntryId,
			Status:  runtimev1pb.TopicEventResponse_RETRY,
		}
		responseEntries[1] = &runtimev1pb.TopicEventBulkResponseEntry{
			EntryId: msgArr[2].EntryId,
			Status:  runtimev1pb.TopicEventResponse_SUCCESS,
		}
		responseEntries[2] = &runtimev1pb.TopicEventBulkResponseEntry{
			EntryId: msgArr[4].EntryId,
			Status:  runtimev1pb.TopicEventResponse_RETRY,
		}
		responseEntries[3] = &runtimev1pb.TopicEventBulkResponseEntry{
			EntryId: msgArr[0].EntryId,
			Status:  runtimev1pb.TopicEventResponse_SUCCESS,
		}
		responseEntries[4] = &runtimev1pb.TopicEventBulkResponseEntry{
			EntryId: msgArr[3].EntryId,
			Status:  runtimev1pb.TopicEventResponse_SUCCESS,
		}
		responses := runtimev1pb.TopicEventBulkResponse{
			Statuses: responseEntries,
		}
		mapResp := make(map[string]*runtimev1pb.TopicEventBulkResponse)
		mapResp["orders"] = &responses
		// create mock application server first
		mockServer := &channelt.MockServer{
			ListTopicSubscriptionsResponse: &subscriptionItems,
			BulkResponsePerPath:            mapResp,
			Error:                          nil,
		}
		grpcServer := startTestAppCallbackAlphaGRPCServer(t, port, mockServer)
		if grpcServer != nil {
			// properly stop the gRPC server
			defer grpcServer.Stop()
		}

		grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
		ps.channels = channels.New(channels.Options{
			ComponentStore:      compstore.New(),
			Registry:            reg,
			GlobalConfig:        new(config.Configuration),
			GRPC:                grpc,
			AppConnectionConfig: config.AppConnectionConfig{Port: port},
		})
		require.NoError(t, err)
		require.NoError(t, ps.channels.Refresh())
		ps.grpc = grpc

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		_, err = ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: false},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: false},
				{EntryId: "5555555e", IsError: true},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("GRPC - verify bulk Subscribe Responses when App sends back wrong entryIds", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         false,
			Channels:       new(channels.Channels),
		})

		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := runtimev1pb.ListTopicSubscriptionsResponse{
			Subscriptions: []*runtimev1pb.TopicSubscription{
				{
					PubsubName: testBulkSubscribePubsub,
					Topic:      "topic0",
					Routes: &runtimev1pb.TopicRoutes{
						Default: "orders",
					},
					BulkSubscribe: &runtimev1pb.BulkSubscribeConfig{Enabled: true},
				},
			},
		}
		msgArr := getBulkMessageEntries(5)
		responseEntries := make([]*runtimev1pb.TopicEventBulkResponseEntry, 5)
		for k, msg := range msgArr {
			responseEntries[k] = &runtimev1pb.TopicEventBulkResponseEntry{
				EntryId: msg.EntryId,
			}
		}
		responseEntries[0].EntryId = "wrongId1"
		responseEntries[3].EntryId = "wrongId2"
		responseEntries = setBulkResponseStatus(responseEntries,
			runtimev1pb.TopicEventResponse_SUCCESS,
			runtimev1pb.TopicEventResponse_RETRY,
			runtimev1pb.TopicEventResponse_SUCCESS,
			runtimev1pb.TopicEventResponse_SUCCESS,
			runtimev1pb.TopicEventResponse_RETRY)

		responses := runtimev1pb.TopicEventBulkResponse{
			Statuses: responseEntries,
		}
		mapResp := make(map[string]*runtimev1pb.TopicEventBulkResponse)
		mapResp["orders"] = &responses
		// create mock application server first
		mockServer := &channelt.MockServer{
			ListTopicSubscriptionsResponse: &subscriptionItems,
			BulkResponsePerPath:            mapResp,
			Error:                          nil,
		}
		grpcServer := startTestAppCallbackAlphaGRPCServer(t, port, mockServer)
		if grpcServer != nil {
			// properly stop the gRPC server
			defer grpcServer.Stop()
		}

		grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
		ps.channels = channels.New(channels.Options{
			ComponentStore:      compstore.New(),
			Registry:            reg,
			GlobalConfig:        new(config.Configuration),
			GRPC:                grpc,
			AppConnectionConfig: config.AppConnectionConfig{Port: port},
		})
		require.NoError(t, err)
		require.NoError(t, ps.channels.Refresh())
		ps.grpc = grpc

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		_, err = ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1111111a", IsError: true},
				{EntryId: "2222222b", IsError: true},
				{EntryId: "333333c", IsError: false},
				{EntryId: "4444444d", IsError: true},
				{EntryId: "5555555e", IsError: true},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})

	t.Run("GRPC - verify bulk Subscribe Response when error while fetching Entry due to wrong dataContentType", func(t *testing.T) {
		port, err := freeport.GetFreePort()
		require.NoError(t, err)
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         false,
			Channels:       new(channels.Channels),
		})

		ps.registry.RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{
					features: []contribpubsub.Feature{contribpubsub.FeatureBulkPublish},
				}
			},
			"mockPubSub",
		)

		subscriptionItems := runtimev1pb.ListTopicSubscriptionsResponse{
			Subscriptions: []*runtimev1pb.TopicSubscription{
				{
					PubsubName: testBulkSubscribePubsub,
					Topic:      "topic0",
					Routes: &runtimev1pb.TopicRoutes{
						Default: "orders",
					},
					BulkSubscribe: &runtimev1pb.BulkSubscribeConfig{Enabled: true},
				},
			},
		}
		msgArr := getBulkMessageEntriesWithWrongData()
		responseEntries := make([]*runtimev1pb.TopicEventBulkResponseEntry, 5)
		for k, msg := range msgArr {
			responseEntries[k] = &runtimev1pb.TopicEventBulkResponseEntry{
				EntryId: msg.EntryId,
			}
		}
		// create mock application server first
		mockServer := &channelt.MockServer{
			ListTopicSubscriptionsResponse: &subscriptionItems,
			BulkResponsePerPath:            nil,
			Error:                          nil,
		}
		grpcServer := startTestAppCallbackAlphaGRPCServer(t, port, mockServer)
		if grpcServer != nil {
			// properly stop the gRPC server
			defer grpcServer.Stop()
		}

		grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
		ps.channels = channels.New(channels.Options{
			ComponentStore:      compstore.New(),
			Registry:            reg,
			GlobalConfig:        new(config.Configuration),
			GRPC:                grpc,
			AppConnectionConfig: config.AppConnectionConfig{Port: port},
		})
		require.NoError(t, ps.channels.Refresh())
		ps.grpc = grpc

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		_, err = ps.BulkPublish(context.TODO(), &contribpubsub.BulkPublishRequest{
			PubsubName: testBulkSubscribePubsub,
			Topic:      "topic0",
			Entries:    msgArr,
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testBulkSubscribePubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)

		expectedResponse := BulkResponseExpectation{
			Responses: []BulkResponseEntryExpectation{
				{EntryId: "1", IsError: true},
			},
		}

		assert.True(t, verifyBulkSubscribeResponses(expectedResponse, pubsubIns.bulkReponse.Statuses))
	})
}

func startTestAppCallbackAlphaGRPCServer(t *testing.T, port int, mockServer *channelt.MockServer) *grpc.Server {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	require.NoError(t, err)
	grpcServer := grpc.NewServer()
	go func() {
		runtimev1pb.RegisterAppCallbackServer(grpcServer, mockServer)
		runtimev1pb.RegisterAppCallbackAlphaServer(grpcServer, mockServer)
		if err := grpcServer.Serve(lis); err != nil {
			panic(err)
		}
	}()
	// wait until server starts
	time.Sleep(testinggrpc.MaxGRPCServerUptime)

	return grpcServer
}

func setBulkResponseStatus(responses []*runtimev1pb.TopicEventBulkResponseEntry,
	status ...runtimev1pb.TopicEventResponse_TopicEventResponseStatus,
) []*runtimev1pb.TopicEventBulkResponseEntry {
	for i, s := range status {
		responses[i].Status = s
	}
	return responses
}

type BulkResponseEntryExpectation struct {
	EntryId string //nolint:stylecheck
	IsError bool
}

type BulkResponseExpectation struct {
	Responses []BulkResponseEntryExpectation
}

func verifyBulkSubscribeResponses(expected BulkResponseExpectation, actual []contribpubsub.BulkSubscribeResponseEntry) bool {
	for i, expectedEntryResponse := range expected.Responses {
		if expectedEntryResponse.EntryId != actual[i].EntryId {
			return false
		}
		if (actual[i].Error != nil) != expectedEntryResponse.IsError {
			return false
		}
	}
	return true
}

func verifyIfEventContainsStrings(event []byte, elems ...string) bool {
	for _, elem := range elems {
		if !strings.Contains(string(event), elem) {
			return false
		}
	}
	return true
}

func verifyIfEventNotContainsStrings(event []byte, elems ...string) bool {
	for _, elem := range elems {
		if strings.Contains(string(event), elem) {
			return false
		}
	}
	return true
}

func verifyBulkSubscribeRequest(expectedData []string, expectedExtension ExpectedExtension,
	actual *runtimev1pb.TopicEventBulkRequest,
) bool {
	for i, expectedEntryReq := range expectedData {
		if expectedEntryReq != string(actual.GetEntries()[i].GetCloudEvent().GetData()) ||
			actual.GetEntries()[i].GetCloudEvent().GetExtensions().GetFields()[expectedExtension.extKey].GetStringValue() != expectedExtension.extValue {
			return false
		}
	}
	return true
}

func assertItemExistsOnce(collection []contribpubsub.BulkSubscribeResponseEntry, items ...string) error {
	count := 0
	for _, item := range items {
		for _, c := range collection {
			if c.EntryId == item {
				count++
			}
		}
		if count != 1 {
			return fmt.Errorf("item %s not found or found more than once", item)
		}
		count = 0
	}
	return nil
}

// mockSubscribePubSub is an in-memory pubsub component.
type mockSubscribePubSub struct {
	bulkHandlers    map[string]contribpubsub.BulkHandler
	handlers        map[string]contribpubsub.Handler
	pubCount        map[string]int
	bulkPubCount    map[string]int
	isBulkSubscribe bool
	bulkReponse     contribpubsub.BulkSubscribeResponse
	features        []contribpubsub.Feature
}

// type BulkSubscribeResponse struct {

// Init is a mock initialization method.
func (m *mockSubscribePubSub) Init(ctx context.Context, metadata contribpubsub.Metadata) error {
	m.bulkHandlers = make(map[string]contribpubsub.BulkHandler)
	m.handlers = make(map[string]contribpubsub.Handler)
	m.pubCount = make(map[string]int)
	m.bulkPubCount = make(map[string]int)
	return nil
}

// Publish is a mock publish method. Immediately trigger handler if topic is subscribed.
func (m *mockSubscribePubSub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	m.pubCount[req.Topic]++
	var err error
	if handler, ok := m.handlers[req.Topic]; ok {
		pubsubMsg := &contribpubsub.NewMessage{
			Data:  req.Data,
			Topic: req.Topic,
		}
		handler(context.Background(), pubsubMsg)
	} else if bulkHandler, ok := m.bulkHandlers[req.Topic]; ok {
		m.bulkPubCount[req.Topic]++
		nbei := contribpubsub.BulkMessageEntry{
			EntryId: "0",
			Event:   req.Data,
		}
		msgArr := []contribpubsub.BulkMessageEntry{nbei}
		nbm := &contribpubsub.BulkMessage{
			Entries: msgArr,
			Topic:   req.Topic,
		}
		_, err = bulkHandler(context.Background(), nbm)
	}
	return err
}

// BulkPublish is a mock publish method. Immediately call the handler for each event in request if topic is subscribed.
func (m *mockSubscribePubSub) BulkPublish(_ context.Context, req *contribpubsub.BulkPublishRequest) (contribpubsub.BulkPublishResponse, error) {
	m.bulkPubCount[req.Topic]++
	res := contribpubsub.BulkPublishResponse{}
	if handler, ok := m.handlers[req.Topic]; ok {
		for _, entry := range req.Entries {
			m.pubCount[req.Topic]++
			// TODO this needs to be modified as part of BulkSubscribe deadletter test
			pubsubMsg := &contribpubsub.NewMessage{
				Data:  entry.Event,
				Topic: req.Topic,
			}
			handler(context.Background(), pubsubMsg)
		}
	} else if bulkHandler, ok := m.bulkHandlers[req.Topic]; ok {
		nbm := &contribpubsub.BulkMessage{
			Entries: req.Entries,
			Topic:   req.Topic,
		}
		bulkResponses, err := bulkHandler(context.Background(), nbm)
		m.bulkReponse.Statuses = bulkResponses
		m.bulkReponse.Error = err
	}

	return res, nil
}

// Subscribe is a mock subscribe method.
func (m *mockSubscribePubSub) Subscribe(_ context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	m.handlers[req.Topic] = handler
	return nil
}

func (m *mockSubscribePubSub) Close() error {
	return nil
}

func (m *mockSubscribePubSub) Features() []contribpubsub.Feature {
	return m.features
}

func (m *mockSubscribePubSub) BulkSubscribe(ctx context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.BulkHandler) error {
	m.isBulkSubscribe = true
	m.bulkHandlers[req.Topic] = handler
	return nil
}

func (m *mockSubscribePubSub) GetBulkResponse() contribpubsub.BulkSubscribeResponse {
	return m.bulkReponse
}

func TestPubSubDeadLetter(t *testing.T) {
	testDeadLetterPubsub := "failPubsub"
	pubsubComponent := componentsV1alpha1.Component{
		ObjectMeta: metav1.ObjectMeta{
			Name: testDeadLetterPubsub,
		},
		Spec: componentsV1alpha1.ComponentSpec{
			Type:     "pubsub.mockPubSub",
			Version:  "v1",
			Metadata: daprt.GetFakeMetadataItems(),
		},
	}

	t.Run("succeeded to publish message to dead letter when send message to app returns error", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		reg.PubSubs().RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{PubsubName: testDeadLetterPubsub, Topic: "topic0", DeadLetterTopic: "topic1", Route: "error"},
			{PubsubName: testDeadLetterPubsub, Topic: "topic1", Route: "success"},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).
			Return(fakeResp, nil)
		// Mock send message to app returns error.
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).
			Return(nil, errors.New("failed to send"))

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		err := ps.Publish(context.TODO(), &contribpubsub.PublishRequest{
			PubsubName: testDeadLetterPubsub,
			Topic:      "topic0",
			Data:       []byte(`{"id":"1"}`),
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testDeadLetterPubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		assert.Equal(t, 1, pubsubIns.pubCount["topic0"])
		// Ensure the message is sent to dead letter topic.
		assert.Equal(t, 1, pubsubIns.pubCount["topic1"])
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 3)
	})

	t.Run("use dead letter with resiliency", func(t *testing.T) {
		reg := registry.New(registry.NewOptions())
		ps := New(Options{
			Registry:       reg.PubSubs(),
			Meta:           meta.New(meta.Options{}),
			Resiliency:     resiliency.New(log),
			ComponentStore: compstore.New(),
			IsHTTP:         true,
			Channels:       new(channels.Channels),
		})
		ps.resiliency = resiliency.FromConfigurations(logger.NewLogger("test"), daprt.TestResiliency)
		reg.PubSubs().RegisterComponent(
			func(_ logger.Logger) contribpubsub.PubSub {
				return &mockSubscribePubSub{}
			},
			"mockPubSub",
		)

		subscriptionItems := []runtimePubsub.SubscriptionJSON{
			{PubsubName: testDeadLetterPubsub, Topic: "topic0", DeadLetterTopic: "topic1", Route: "error"},
			{PubsubName: testDeadLetterPubsub, Topic: "topic1", Route: "success"},
		}
		sub, _ := json.Marshal(subscriptionItems)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(sub).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels.WithAppChannel(mockAppChannel)
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), matchDaprRequestMethod("dapr/subscribe")).
			Return(fakeResp, nil)
		// Mock send message to app returns error.
		mockAppChannel.
			On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).
			Return(nil, errors.New("failed to send"))

		require.NoError(t, ps.Init(context.TODO(), pubsubComponent))
		require.NoError(t, ps.StartSubscriptions(context.TODO()))

		err := ps.Publish(context.TODO(), &contribpubsub.PublishRequest{
			PubsubName: testDeadLetterPubsub,
			Topic:      "topic0",
			Data:       []byte(`{"id":"1"}`),
		})
		require.NoError(t, err)
		pubSub, ok := ps.compStore.GetPubSub(testDeadLetterPubsub)
		require.True(t, ok)
		pubsubIns := pubSub.Component.(*mockSubscribePubSub)
		// Consider of resiliency, publish message may retry in some cases, make sure the pub count is greater than 1.
		assert.GreaterOrEqual(t, pubsubIns.pubCount["topic0"], 1)
		// Make sure every message that is sent to topic0 is sent to its dead letter topic1.
		assert.Equal(t, pubsubIns.pubCount["topic0"], pubsubIns.pubCount["topic1"])
		// Except of the one getting config from app, make sure each publish will result to twice subscribe call
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1+2*pubsubIns.pubCount["topic0"]+2*pubsubIns.pubCount["topic1"])
	})
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
