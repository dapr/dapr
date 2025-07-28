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

package subscription

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/http"
)

func TestTracingOnNewPublishedMessage(t *testing.T) {
	t.Run("succeeded to publish message with TraceParent in metadata", func(t *testing.T) {
		comp := &mockSubscribePubSub{}
		require.NoError(t, comp.Init(t.Context(), contribpubsub.Metadata{}))

		resp := contribpubsub.AppResponse{
			Status: contribpubsub.Success,
		}

		respB, _ := json.Marshal(resp)
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(respB).
			WithContentType("application/json")
		defer fakeResp.Close()

		for _, rawPayload := range []bool{false, true} {
			mockAppChannel := new(channelt.MockAppChannel)
			mockAppChannel.Init()
			mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

			ps, err := New(Options{
				Resiliency: resiliency.New(log),
				Postman: http.New(http.Options{
					Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
				}),
				PubSub:     &runtimePubsub.PubsubItem{Component: comp},
				AppID:      TestRuntimeConfigID,
				PubSubName: "testpubsub",
				Topic:      "topic0",
				Route: runtimePubsub.Subscription{
					Metadata: map[string]string{"rawPayload": strconv.FormatBool(rawPayload)},
					Rules: []*runtimePubsub.Rule{
						{Path: "orders"},
					},
					DeadLetterTopic: "topic1",
				},
			})
			require.NoError(t, err)
			t.Cleanup(func() {
				ps.Stop()
			})

			traceparent := "00-0af7651916cd43dd8448eb211c80319c-b9c7c989f97918e1-01"
			traceid := "00-80e1afed08e019fc1110464cfa66635c-7a085853722dc6d2-01"
			tracestate := "abc=xyz"
			err = comp.Publish(t.Context(), &contribpubsub.PublishRequest{
				PubsubName: "testpubsub",
				Topic:      "topic0",
				Data:       []byte(`{"orderId":"1"}`),
				Metadata:   map[string]string{contribpubsub.TraceParentField: traceparent, contribpubsub.TraceIDField: traceid, contribpubsub.TraceStateField: tracestate},
			})
			require.NoError(t, err)
			reqs := mockAppChannel.GetInvokedRequest()
			reqMetadata := mockAppChannel.GetInvokedRequestMetadata()
			mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
			assert.Contains(t, reqMetadata["orders"][contribpubsub.TraceParentField], traceparent)
			assert.Contains(t, reqMetadata["orders"][contribpubsub.TraceStateField], tracestate)
			if rawPayload {
				assert.Contains(t, string(reqs["orders"]), `{"data_base64":"eyJvcmRlcklkIjoiMSJ9"`)
				// traceparent also included as part of a CloudEvent
				assert.Contains(t, string(reqs["orders"]), traceparent)
				assert.Contains(t, string(reqs["orders"]), tracestate)
				// traceid is superseded by traceparent
				assert.NotContains(t, string(reqs["orders"]), traceid)
			} else {
				assert.Contains(t, string(reqs["orders"]), `{"orderId":"1"}`)
			}
		}
	})
}
