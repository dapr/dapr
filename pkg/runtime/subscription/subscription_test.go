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
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/http"
	"github.com/dapr/dapr/pkg/runtime/subscription/postman/streaming"
	"github.com/dapr/kit/logger"
)

// mockAdapterStreamer implements runtimePubsub.AdapterStreamer and captures the published message.
type mockAdapterStreamer struct {
	mock.Mock
	receivedMessage *runtimePubsub.SubscribedMessage
}

func (f *mockAdapterStreamer) Publish(ctx context.Context, sm *runtimePubsub.SubscribedMessage) (*rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1, error) {
	f.receivedMessage = sm
	args := f.Called(ctx, sm)
	return args.Get(0).(*rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1), args.Error(1)
}

func (f *mockAdapterStreamer) StreamerKey(pubsubName, topic string) string {
	return f.Called(pubsubName, topic).String(0)
}

func (f *mockAdapterStreamer) Close(key string, connectionID runtimePubsub.ConnectionID) {
	f.Called(key, connectionID)
}

func (f *mockAdapterStreamer) Subscribe(server rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server, request *rtv1pb.SubscribeTopicEventsRequestInitialAlpha1, connectionID runtimePubsub.ConnectionID) error {
	return f.Called(server, request, connectionID).Error(0)
}

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
			t.Cleanup(ps.Stop)

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

func TestCloudEventPayload(t *testing.T) {
	// Prepare a JSON payload that will be unmarshaled.
	originalData := map[string]interface{}{"foo": "bar"}
	dataBytes, err := json.Marshal(originalData)
	require.NoError(t, err)

	pubSub := &mockSubscribePubSub{}
	require.NoError(t, pubSub.Init(t.Context(), contribpubsub.Metadata{}))

	resp := contribpubsub.AppResponse{
		Status: contribpubsub.Success,
	}
	respB, _ := json.Marshal(resp)

	t.Run("verify payload and metadata are included in the cloud event for non-raw routes", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(respB).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockStreamer := &mockAdapterStreamer{}
		mockStreamer.On("Publish", mock.Anything, mock.Anything).Return(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{}, nil)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

		// Create subscription options.
		opts := Options{
			AppID:      "test-app",
			PubSubName: "fake-pubsub",
			Topic:      "test-topic",
			PubSub:     &runtimePubsub.PubsubItem{Component: pubSub},
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Postman: streaming.New(streaming.Options{
				Channel: mockStreamer,
			}),
			Route: runtimePubsub.Subscription{
				Metadata: map[string]string{},
				Rules:    []*runtimePubsub.Rule{{Path: "/"}},
			},
		}

		s, err := New(opts)
		require.NoError(t, err)
		defer s.Stop()

		// Simulate a message
		err = pubSub.Publish(t.Context(), &contribpubsub.PublishRequest{
			PubsubName: "fake-pubsub",
			Topic:      "test-topic",
			Data:       dataBytes,
			Metadata:   map[string]string{"__key": "key-value"},
		})
		require.NoError(t, err)

		// Check that the original data is properly unmarshaled into the DataField.
		cloudEvent := mockStreamer.receivedMessage.CloudEvent
		if de, exists := cloudEvent[contribpubsub.DataField]; !exists {
			t.Error("missing DataField in CloudEvent")
		} else {
			got, ok := de.(map[string]interface{})
			if !ok {
				t.Errorf("expected DataField to be map[string]interface{}, got %T", de)
			}
			if v, ok := got["foo"]; !ok || v != "bar" {
				t.Errorf("expected DataField to contain foo=bar, got: %v", got)
			}
		}

		// Check that DataContentTypeField is set to "application/json"
		if v, exists := cloudEvent[contribpubsub.DataContentTypeField]; !exists || v != "application/json" {
			t.Errorf("expected DataContentTypeField to be 'application/json', got: %v", v)
		}

		// Verify that the remaining metadata has been copied as extensions.
		expectedMetadata := map[string]string{
			"_metadata___key":      "key-value",
			"_metadata_pubsubName": "fake-pubsub",
		}
		for k, v := range expectedMetadata {
			if cloudEvent[k] != v {
				t.Errorf("expected CloudEvent to contain %s=%s, got: %v", k, v, cloudEvent[k])
			}
		}
	})

	t.Run("verify cloud event is not modified for non-raw routes", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(respB).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockStreamer := &mockAdapterStreamer{}
		mockStreamer.On("Publish", mock.Anything, mock.Anything).Return(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{}, nil)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

		// Create subscription options.
		opts := Options{
			AppID:      "test-app",
			PubSubName: "fake-pubsub",
			Topic:      "test-topic",
			PubSub:     &runtimePubsub.PubsubItem{Component: pubSub},
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Postman: streaming.New(streaming.Options{
				Channel: mockStreamer,
			}),
			Route: runtimePubsub.Subscription{
				Metadata: map[string]string{},
				Rules:    []*runtimePubsub.Rule{{Path: "/"}},
			},
		}

		s, err := New(opts)
		require.NoError(t, err)
		defer s.Stop()

		// Simulate a message
		cloudEventInput := map[string]interface{}{
			contribpubsub.DataContentTypeField: "application/json",
			contribpubsub.IDField:              "123",
			contribpubsub.SpecVersionField:     "1.0",
			contribpubsub.TypeField:            "test",
			contribpubsub.DataField:            "{\"foo\":\"bar\"}",
		}
		cloudEventBytes, err := json.Marshal(cloudEventInput)
		require.NoError(t, err)
		err = pubSub.Publish(t.Context(), &contribpubsub.PublishRequest{
			PubsubName: "fake-pubsub",
			Topic:      "test-topic",
			Data:       cloudEventBytes,
			Metadata:   map[string]string{"__key": "key-value"},
		})
		require.NoError(t, err)

		// Check that the original cloud event is not modified except the extra metadata
		cloudEventInput["_metadata___key"] = "key-value"
		cloudEventInput["_metadata_pubsubName"] = "fake-pubsub"
		cloudEvent := mockStreamer.receivedMessage.CloudEvent
		assert.Equal(t, cloudEventInput, cloudEvent)
	})

	t.Run("verify payload and metadata are included in the cloud event for raw routes", func(t *testing.T) {
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataBytes(respB).
			WithContentType("application/json")
		defer fakeResp.Close()

		mockStreamer := &mockAdapterStreamer{}
		mockStreamer.On("Publish", mock.Anything, mock.Anything).Return(&rtv1pb.SubscribeTopicEventsRequestProcessedAlpha1{}, nil)
		mockAppChannel := new(channelt.MockAppChannel)
		mockAppChannel.Init()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), mock.Anything).Return(fakeResp, nil)

		// Create subscription options.
		opts := Options{
			AppID:      "test-app",
			PubSubName: "fake-pubsub",
			Topic:      "test-topic",
			PubSub:     &runtimePubsub.PubsubItem{Component: pubSub},
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Postman: streaming.New(streaming.Options{
				Channel: mockStreamer,
			}),
			Route: runtimePubsub.Subscription{
				Metadata: map[string]string{"rawPayload": "true"},
				Rules:    []*runtimePubsub.Rule{{Path: "/"}},
			},
		}

		s, err := New(opts)
		require.NoError(t, err)
		defer s.Stop()

		// Simulate a message
		err = pubSub.Publish(t.Context(), &contribpubsub.PublishRequest{
			PubsubName: "fake-pubsub",
			Topic:      "test-topic",
			Data:       dataBytes,
			Metadata:   map[string]string{"__key": "key-value"},
		})
		require.NoError(t, err)

		// Verify that the published message was captured by our fake adapter streamer.
		sm := mockStreamer.receivedMessage
		if sm == nil {
			t.Fatal("expected a subscribed message to be published, but got nil")
		}

		// Check that the original data is properly unmarshaled into the DataBase64Field.
		cloudEvent := sm.CloudEvent
		if de, exists := cloudEvent[contribpubsub.DataBase64Field]; !exists {
			t.Error("missing DataBase64Field in CloudEvent")
		} else {
			// Decode the base64 data
			decodedData, err := base64.StdEncoding.DecodeString(de.(string))
			require.NoError(t, err)

			// Unmarshal the decoded data into a map
			var got map[string]interface{}
			require.NoError(t, json.Unmarshal(decodedData, &got))

			if v, ok := got["foo"]; !ok || v != "bar" {
				t.Errorf("expected DataField to contain foo=bar, got: %v", got)
			}
		}

		// Check that DataContentTypeField is set to "application/json"
		if v, exists := cloudEvent[contribpubsub.DataContentTypeField]; !exists || v != "application/octet-stream" {
			t.Errorf("expected DataContentTypeField to be 'application/json', got: %v", v)
		}

		// Also verify that the remaining metadata has been copied as extensions.
		// For example, the pubsub name should be added as an extension if not already present.
		expectedMetadata := map[string]string{
			"_metadata___key":      "key-value",
			"_metadata_pubsubName": "fake-pubsub",
		}
		for k, v := range expectedMetadata {
			if cloudEvent[k] != v {
				t.Errorf("expected CloudEvent to contain %s=%s, got: %v", k, v, cloudEvent[k])
			}
		}
	})
}
