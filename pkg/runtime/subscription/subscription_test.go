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

package subscription

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	rtv1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	rtpubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
	"github.com/stretchr/testify/require"
)

// fakeAdapterStreamer implements rtpubsub.AdapterStreamer and captures the published message.
type fakeAdapterStreamer struct {
	receivedMessage *rtpubsub.SubscribedMessage
}

func (f *fakeAdapterStreamer) Publish(ctx context.Context, sm *rtpubsub.SubscribedMessage) error {
	f.receivedMessage = sm
	return nil
}

func (f *fakeAdapterStreamer) StreamerKey(pubsubName, topic string) string {
	return "fake-key"
}

func (f *fakeAdapterStreamer) Close(key string, connectionID rtpubsub.ConnectionID) {
	// no‑op for testing
}

func (f *fakeAdapterStreamer) Subscribe(server rtv1pb.Dapr_SubscribeTopicEventsAlpha1Server, request *rtv1pb.SubscribeTopicEventsRequestInitialAlpha1, connectionID rtpubsub.ConnectionID) error {
	// no-op for testing
	return nil
}

// fakePubSub is a fake implementation of the PubSub interface for testing purposes.
type fakePubSub struct {
	subscribeHandler contribpubsub.Handler
}

func (f *fakePubSub) Init(ctx context.Context, metadata contribpubsub.Metadata) error {
	// No-op for testing
	return nil
}

func (f *fakePubSub) Features() []contribpubsub.Feature {
	// Return an empty feature set for testing
	return []contribpubsub.Feature{}
}

func (f *fakePubSub) Publish(ctx context.Context, req *contribpubsub.PublishRequest) error {
	// Capture the published message for testing
	return nil
}

func (f *fakePubSub) Subscribe(ctx context.Context, req contribpubsub.SubscribeRequest, handler contribpubsub.Handler) error {
	// Store the handler for testing
	f.subscribeHandler = handler
	return nil
}

func (f *fakePubSub) Close() error {
	// No-op for testing
	return nil
}

func Test_CloudEventPayload(t *testing.T) {
	// Prepare a JSON payload that will be unmarshaled.
	originalData := map[string]interface{}{"foo": "bar"}
	dataBytes, err := json.Marshal(originalData)
	require.NoError(t, err)

	pubSub := fakePubSub{}

	t.Run("verify payload and metadata are included in the cloud event for non-raw routes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		fakeStreamer := &fakeAdapterStreamer{}

		// Create subscription options.
		opts := Options{
			AppID:      "test-app",
			PubSubName: "fake-pubsub",
			Topic:      "test-topic",
			IsHTTP:     false,
			PubSub:     &rtpubsub.PubsubItem{Component: &pubSub},
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Route: rtpubsub.Subscription{
				Metadata: map[string]string{},
				Rules:    []*rtpubsub.Rule{{Path: "/"}},
			},
			AdapterStreamer: fakeStreamer,
		}

		s, err := New(opts)
		require.NoError(t, err)
		require.NotNil(t, pubSub.subscribeHandler)

		// Prepare a JSON payload that will be unmarshaled.
		originalData := map[string]interface{}{"foo": "bar"}
		dataBytes, err := json.Marshal(originalData)
		require.NoError(t, err)

		// Simulate a message
		contentType := "application/json"
		pubSub.subscribeHandler(ctx, &contribpubsub.NewMessage{
			Topic:       "test-topic",
			Data:        dataBytes,
			Metadata:    map[string]string{"__key": "key-value"},
			ContentType: &contentType,
		})

		// Verify that the published message was captured by our fake adapter streamer.
		sm := fakeStreamer.receivedMessage
		if sm == nil {
			t.Fatal("expected a subscribed message to be published, but got nil")
		}

		// Check that the original data is properly unmarshaled into the DataField.
		cloudEvent := sm.CloudEvent
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

		s.Stop()
	})

	t.Run("verify payload and metadata are included in the cloud event for raw routes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)

		fakeStreamer := &fakeAdapterStreamer{}

		// Create subscription options.
		opts := Options{
			AppID:      "test-app",
			PubSubName: "fake-pubsub",
			Topic:      "test-topic",
			IsHTTP:     false,
			PubSub:     &rtpubsub.PubsubItem{Component: &pubSub},
			Resiliency: resiliency.New(logger.NewLogger("test")),
			Route: rtpubsub.Subscription{
				Metadata: map[string]string{"rawPayload": "true"},
				Rules:    []*rtpubsub.Rule{{Path: "/"}},
			},
			AdapterStreamer: fakeStreamer,
		}

		s, err := New(opts)
		require.NoError(t, err)
		require.NotNil(t, pubSub.subscribeHandler)

		// Simulate a message
		contentType := "application/json"
		pubSub.subscribeHandler(ctx, &contribpubsub.NewMessage{
			Topic:       "test-topic",
			Data:        dataBytes,
			Metadata:    map[string]string{"__key": "key-value"},
			ContentType: &contentType,
		})

		// Verify that the published message was captured by our fake adapter streamer.
		sm := fakeStreamer.receivedMessage
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

		s.Stop()
	})
}
