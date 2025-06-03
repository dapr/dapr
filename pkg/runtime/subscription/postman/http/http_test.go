/*
Copyright 2025 The Dapr Authors
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

package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/contenttype"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/runtime/channels"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/kit/logger"
)

func TestErrorPublishedNonCloudEventHTTP(t *testing.T) {
	topic := "topic1"

	testPubSubMessage := &runtimePubsub.SubscribedMessage{
		CloudEvent: map[string]interface{}{},
		Topic:      topic,
		Data:       []byte("testing"),
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
		PubSub:     "testpubsub",
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.Topic).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(testPubSubMessage.Data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(testPubSubMessage.Metadata)
	defer fakeReq.Close()

	t.Run("ok without result body", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)

		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		var appResp contribpubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)
		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage))
	})

	t.Run("ok with empty body", func(t *testing.T) {
		log.SetOutputLevel(logger.DebugLevel)
		defer log.SetOutputLevel(logger.InfoLevel)

		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)
		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage))
	})

	t.Run("ok with retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"RETRY\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		require.Error(t, h.Deliver(t.Context(), testPubSubMessage))
	})

	t.Run("ok with drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"DROP\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		assert.Equal(t, runtimePubsub.ErrMessageDropped, h.Deliver(t.Context(), testPubSubMessage))
	})

	t.Run("ok with unknown", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"UNKNOWN\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		require.Error(t, h.Deliver(t.Context(), testPubSubMessage))
	})

	t.Run("not found response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage))
	})
}

func TestOnNewPublishedMessage(t *testing.T) {
	topic := "topic1"

	matchContextInterface := func(v any) bool {
		_, ok := v.(context.Context)
		return ok
	}

	envelope := contribpubsub.NewCloudEventsEnvelope("", "", contribpubsub.DefaultCloudEventType, "", topic,
		"testpubsub2", "", []byte("Test Message"), "", "")
	b, err := json.Marshal(envelope)
	require.NoError(t, err)

	testPubSubMessage := &runtimePubsub.SubscribedMessage{
		CloudEvent: envelope,
		Topic:      topic,
		Data:       b,
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
	}

	fakeReq := invokev1.NewInvokeMethodRequest(testPubSubMessage.Topic).
		WithHTTPExtension(nethttp.MethodPost, "").
		WithRawDataBytes(testPubSubMessage.Data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(testPubSubMessage.Metadata)
	defer fakeReq.Close()

	t.Run("succeeded to publish message to user app with empty response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		var appResp contribpubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message without TraceID", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		var appResp contribpubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		// Generate a new envelope to avoid affecting other tests by modifying shared `envelope`
		envelopeNoTraceID := contribpubsub.NewCloudEventsEnvelope(
			"", "", contribpubsub.DefaultCloudEventType, "", topic, "testpubsub2", "",
			[]byte("Test Message"), "", "")
		delete(envelopeNoTraceID, contribpubsub.TraceIDField)
		bNoTraceID, err := json.Marshal(envelopeNoTraceID)
		require.NoError(t, err)

		message := &runtimePubsub.SubscribedMessage{
			CloudEvent: envelopeNoTraceID,
			Topic:      topic,
			Data:       bNoTraceID,
			Metadata:   map[string]string{"pubsubName": "testpubsub"},
			Path:       "topic1",
		}

		fakeReqNoTraceID := invokev1.NewInvokeMethodRequest(message.Topic).
			WithHTTPExtension(nethttp.MethodPost, "").
			WithRawDataBytes(message.Data).
			WithContentType(contenttype.CloudEventContentType).
			WithCustomHTTPMetadata(testPubSubMessage.Metadata)
		defer fakeReqNoTraceID.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReqNoTraceID).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), message))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with non-json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with status", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"SUCCESS\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask for retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"RETRY\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent["id"].(string), rterrors.NewRetriable(nil))
		assert.Equal(t, expectedClientError.Error(), h.Deliver(t.Context(), testPubSubMessage).Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask to drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"DROP\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		assert.Equal(t, runtimePubsub.ErrMessageDropped, h.Deliver(t.Context(), testPubSubMessage))
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned unknown status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"not_valid\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		require.Error(t, h.Deliver(t.Context(), testPubSubMessage), "expected error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned empty status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"message\": \"empty status\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage), "expected no error on empty status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app and app returned unexpected json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"message\": \"success\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		require.NoError(t, h.Deliver(t.Context(), testPubSubMessage), "expected no error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message error on invoking method", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		invokeError := errors.New("error invoking method")

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(nil, invokeError)

		err := h.Deliver(t.Context(), testPubSubMessage)
		expectedError := fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(invokeError))
		assert.Equal(t, expectedError.Error(), err.Error(), "expected errors to match")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 404", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil).
			WithRawDataString("Not found").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		err := h.Deliver(t.Context(), testPubSubMessage)

		require.NoError(t, err, "expected error to be nil")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 500", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		h := New(Options{
			Channels: new(channels.Channels).WithAppChannel(mockAppChannel),
		})

		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil).
			WithRawDataString("Internal Error").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		err := h.Deliver(t.Context(), testPubSubMessage)

		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: Internal Error. status code returned: 500", cloudEvent["id"].(string), cloudEvent["topic"])
		expectedClientError := rterrors.NewRetriable(errors.New(errMsg))
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}
