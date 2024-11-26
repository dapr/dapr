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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"testing"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dapr/components-contrib/contenttype"
	contribpubsub "github.com/dapr/components-contrib/pubsub"
	inmemory "github.com/dapr/components-contrib/pubsub/in-memory"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	invokev1 "github.com/dapr/dapr/pkg/messaging/v1"
	"github.com/dapr/dapr/pkg/modes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/pkg/resiliency"
	"github.com/dapr/dapr/pkg/runtime/channels"
	"github.com/dapr/dapr/pkg/runtime/compstore"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	runtimePubsub "github.com/dapr/dapr/pkg/runtime/pubsub"
	"github.com/dapr/dapr/pkg/runtime/registry"
	testinggrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/logger"
	"github.com/dapr/kit/ptr"
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
		WithHTTPExtension(http.MethodPost, "").
		WithRawDataBytes(testPubSubMessage.Data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(testPubSubMessage.Metadata)
	defer fakeReq.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	comp := inmemory.New(log)
	require.NoError(t, comp.Init(ctx, contribpubsub.Metadata{}))

	ps, err := New(Options{
		IsHTTP:     true,
		Resiliency: resiliency.New(logger.NewLogger("test")),
		Namespace:  "ns1",
		PubSub:     &runtimePubsub.PubsubItem{Component: comp},
	})
	require.NoError(t, err)

	t.Run("ok without result body", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel

		var appResp contribpubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err)
	})

	t.Run("ok with empty body", func(t *testing.T) {
		log.SetOutputLevel(logger.DebugLevel)
		defer log.SetOutputLevel(logger.InfoLevel)

		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err)
	})

	t.Run("ok with retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"RETRY\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.Error(t, err)
	})

	t.Run("ok with drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"DROP\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Equal(t, runtimePubsub.ErrMessageDropped, err)
	})

	t.Run("ok with unknown", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"UNKNOWN\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.Error(t, err)
	})

	t.Run("not found response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel

		fakeResp := invokev1.NewInvokeMethodResponse(404, "NotFound", nil)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.Anything, fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err)
	})
}

func TestErrorPublishedNonCloudEventGRPC(t *testing.T) {
	topic := "topic1"

	testPubSubMessage := &runtimePubsub.SubscribedMessage{
		CloudEvent: map[string]interface{}{},
		Topic:      topic,
		Data:       []byte("testing"),
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	comp := inmemory.New(log)
	require.NoError(t, comp.Init(ctx, contribpubsub.Metadata{}))

	ps, err := New(Options{
		AppID:      "test",
		Namespace:  "ns1",
		PubSubName: "testpubsub",
		Topic:      topic,
		IsHTTP:     false,
		PubSub:     &runtimePubsub.PubsubItem{Component: comp},
		Resiliency: resiliency.New(logger.NewLogger("test")),
		GRPC:       manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{}),
	})
	require.NoError(t, err)

	testcases := []struct {
		Name        string
		Status      runtimev1pb.TopicEventResponse_TopicEventResponseStatus
		Error       error
		ExpectError bool
	}{
		{
			Name:   "ok without success",
			Status: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			Name:        "ok with retry",
			Status:      runtimev1pb.TopicEventResponse_RETRY,
			ExpectError: true,
		},
		{
			Name:        "ok with drop",
			Status:      runtimev1pb.TopicEventResponse_DROP,
			ExpectError: true,
		},
		{
			Name:        "ok with unknown",
			Status:      runtimev1pb.TopicEventResponse_TopicEventResponseStatus(999),
			ExpectError: true,
		},
		{
			Name:        "ok with error",
			Error:       errors.New("TEST"),
			ExpectError: true,
		},
	}

	for _, tc := range testcases {
		t.Run(tc.Name, func(t *testing.T) {
			mockClientConn := channelt.MockClientConn{
				InvokeFn: func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...grpc.CallOption) error {
					if tc.Error != nil {
						return tc.Error
					}

					response, ok := reply.(*runtimev1pb.TopicEventResponse)
					if !ok {
						return fmt.Errorf("unexpected reply type: %s", reflect.TypeOf(reply))
					}

					response.Status = tc.Status

					return nil
				},
			}
			ps.grpc.SetAppClientConn(&mockClientConn)

			err := ps.publishMessageGRPC(context.Background(), testPubSubMessage)
			if tc.ExpectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOnNewPublishedMessage(t *testing.T) {
	topic := "topic1"

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
		WithHTTPExtension(http.MethodPost, "").
		WithRawDataBytes(testPubSubMessage.Data).
		WithContentType(contenttype.CloudEventContentType).
		WithCustomHTTPMetadata(testPubSubMessage.Metadata)
	defer fakeReq.Close()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	comp := inmemory.New(log)
	require.NoError(t, comp.Init(ctx, contribpubsub.Metadata{}))

	ps, err := New(Options{
		IsHTTP:     true,
		Resiliency: resiliency.New(logger.NewLogger("test")),
		Namespace:  "ns1",
		PubSub:     &runtimePubsub.PubsubItem{Component: comp},
		AppID:      "consumer0",
	})
	require.NoError(t, err)

	t.Run("succeeded to publish message to user app with empty response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		var appResp contribpubsub.AppResponse
		var buf bytes.Buffer
		require.NoError(t, json.NewEncoder(&buf).Encode(appResp))
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).WithRawData(&buf)
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message without TraceID", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

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
			WithHTTPExtension(http.MethodPost, "").
			WithRawDataBytes(message.Data).
			WithContentType(contenttype.CloudEventContentType).
			WithCustomHTTPMetadata(testPubSubMessage.Metadata)
		defer fakeReqNoTraceID.Close()
		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReqNoTraceID).Return(fakeResp, nil)

		// act
		err = ps.publishMessageHTTP(context.Background(), message)

		// assert
		require.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with non-json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("OK").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app with status", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"SUCCESS\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask for retry", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"RETRY\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		expectedClientError := fmt.Errorf("RETRY status returned from app while processing pub/sub event %v: %w", cloudEvent["id"].(string), rterrors.NewRetriable(nil))
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app ask to drop", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"DROP\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		assert.Equal(t, runtimePubsub.ErrMessageDropped, err)
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned unknown status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"status\": \"not_valid\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.Error(t, err, "expected error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app but app returned empty status code", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"message\": \"empty status\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err, "expected no error on empty status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("succeeded to publish message to user app and app returned unexpected json response", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		// User App subscribes 1 topics via http app channel
		fakeResp := invokev1.NewInvokeMethodResponse(200, "OK", nil).
			WithRawDataString("{ \"message\": \"success\"}").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err, "expected no error on unknown status")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message error on invoking method", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)
		invokeError := errors.New("error invoking method")

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(nil, invokeError)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		expectedError := fmt.Errorf("error returned from app channel while sending pub/sub event to app: %w", rterrors.NewRetriable(invokeError))
		assert.Equal(t, expectedError.Error(), err.Error(), "expected errors to match")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 404", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeResp := invokev1.NewInvokeMethodResponse(404, "Not Found", nil).
			WithRawDataString("Not found").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		require.NoError(t, err, "expected error to be nil")
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})

	t.Run("failed to publish message to user app with 500", func(t *testing.T) {
		mockAppChannel := new(channelt.MockAppChannel)
		ps.channels = new(channels.Channels).WithAppChannel(mockAppChannel)

		fakeResp := invokev1.NewInvokeMethodResponse(500, "Internal Error", nil).
			WithRawDataString("Internal Error").
			WithContentType("application/json")
		defer fakeResp.Close()

		mockAppChannel.On("InvokeMethod", mock.MatchedBy(matchContextInterface), fakeReq).Return(fakeResp, nil)

		// act
		err := ps.publishMessageHTTP(context.Background(), testPubSubMessage)

		// assert
		var cloudEvent map[string]interface{}
		json.Unmarshal(testPubSubMessage.Data, &cloudEvent)
		errMsg := fmt.Sprintf("retriable error returned from app while processing pub/sub event %v, topic: %v, body: Internal Error. status code returned: 500", cloudEvent["id"].(string), cloudEvent["topic"])
		expectedClientError := rterrors.NewRetriable(errors.New(errMsg))
		assert.Equal(t, expectedClientError.Error(), err.Error())
		mockAppChannel.AssertNumberOfCalls(t, "InvokeMethod", 1)
	})
}

func TestOnNewPublishedMessageGRPC(t *testing.T) {
	topic := "topic1"

	envelope := contribpubsub.NewCloudEventsEnvelope("", "", contribpubsub.DefaultCloudEventType, "", topic,
		"testpubsub2", "", []byte("Test Message"), "", "")
	// add custom attributes
	envelope["customInt"] = 123
	envelope["customString"] = "abc"
	envelope["customBool"] = true
	envelope["customFloat"] = 1.23
	envelope["customArray"] = []interface{}{"a", "b", 789, 3.1415}
	envelope["customMap"] = map[string]interface{}{"a": "b", "c": 456}
	b, err := json.Marshal(envelope)
	require.NoError(t, err)

	testPubSubMessage := &runtimePubsub.SubscribedMessage{
		CloudEvent: envelope,
		Topic:      topic,
		Data:       b,
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
	}

	envelope = contribpubsub.NewCloudEventsEnvelope("", "", contribpubsub.DefaultCloudEventType, "", topic,
		"testpubsub2", "application/octet-stream", []byte{0x1}, "", "")
	// add custom attributes
	envelope["customInt"] = 123
	envelope["customString"] = "abc"
	envelope["customBool"] = true
	envelope["customFloat"] = 1.23
	envelope["customArray"] = []interface{}{"a", "b", 789, 3.1415}
	envelope["customMap"] = map[string]interface{}{"a": "b", "c": 456}
	base64, err := json.Marshal(envelope)
	require.NoError(t, err)

	testPubSubMessageBase64 := &runtimePubsub.SubscribedMessage{
		CloudEvent: envelope,
		Topic:      topic,
		Data:       base64,
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
	}

	testCases := []struct {
		name                        string
		message                     *runtimePubsub.SubscribedMessage
		responseStatus              runtimev1pb.TopicEventResponse_TopicEventResponseStatus
		expectedError               error
		noResponseStatus            bool
		responseError               error
		validateCloudEventExtension *map[string]interface{}
	}{
		{
			name:             "failed to publish message to user app with unimplemented error",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    status.Errorf(codes.Unimplemented, "unimplemented method"),
		},
		{
			name:             "failed to publish message to user app with response error",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    assert.AnError,
			expectedError: fmt.Errorf(
				"error returned from app while processing pub/sub event %v: %w",
				testPubSubMessage.CloudEvent[contribpubsub.IDField],
				rterrors.NewRetriable(status.Error(codes.Unknown, assert.AnError.Error())),
			),
		},
		{
			name:             "succeeded to publish message to user app with empty response",
			message:          testPubSubMessage,
			noResponseStatus: true,
		},
		{
			name:           "succeeded to publish message to user app with success response",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			name:           "succeeded to publish message to user app with base64 encoded cloud event",
			message:        testPubSubMessageBase64,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
		},
		{
			name:           "succeeded to publish message to user app with retry",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_RETRY,
			expectedError: fmt.Errorf(
				"RETRY status returned from app while processing pub/sub event %v: %w",
				testPubSubMessage.CloudEvent[contribpubsub.IDField],
				rterrors.NewRetriable(nil),
			),
		},
		{
			name:           "succeeded to publish message to user app with drop",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_DROP,
			expectedError:  runtimePubsub.ErrMessageDropped,
		},
		{
			name:           "succeeded to publish message to user app with invalid response",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_TopicEventResponseStatus(99),
			expectedError: fmt.Errorf(
				"unknown status returned from app while processing pub/sub event %v, status: %v, err: %w",
				testPubSubMessage.CloudEvent[contribpubsub.IDField],
				runtimev1pb.TopicEventResponse_TopicEventResponseStatus(99),
				rterrors.NewRetriable(nil),
			),
		},
		{
			name:           "succeeded to publish message to user app and validated cloud event extension attributes",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
			validateCloudEventExtension: ptr.Of(map[string]interface{}{
				"customInt":    float64(123),
				"customString": "abc",
				"customBool":   true,
				"customFloat":  float64(1.23),
				"customArray":  []interface{}{"a", "b", float64(789), float64(3.1415)},
				"customMap":    map[string]interface{}{"a": "b", "c": float64(456)},
			}),
		},
		{
			name:           "succeeded to publish message to user app and validated cloud event extension attributes with base64 encoded data",
			message:        testPubSubMessageBase64,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
			validateCloudEventExtension: ptr.Of(map[string]interface{}{
				"customInt":    float64(123),
				"customString": "abc",
				"customBool":   true,
				"customFloat":  float64(1.23),
				"customArray":  []interface{}{"a", "b", float64(789), float64(3.1415)},
				"customMap":    map[string]interface{}{"a": "b", "c": float64(456)},
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// setup
			// getting new port for every run to avoid conflict and timing issues between tests if sharing same port

			port, err := freeport.GetFreePort()
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			comp := inmemory.New(log)
			require.NoError(t, comp.Init(ctx, contribpubsub.Metadata{}))

			reg := registry.New(registry.NewOptions())
			ps, err := New(Options{
				IsHTTP:     true,
				Resiliency: resiliency.New(logger.NewLogger("test")),
				Namespace:  "ns1",
				PubSub:     &runtimePubsub.PubsubItem{Component: comp},
				Topic:      topic,
				PubSubName: "testpubsub",
				AppID:      "consumer0",
				GRPC:       manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port}),
			})
			require.NoError(t, err)

			var grpcServer *grpc.Server

			// create mock application server first
			if !tc.noResponseStatus {
				grpcServer = testinggrpc.StartTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
					TopicEventResponseStatus:    tc.responseStatus,
					Error:                       tc.responseError,
					ValidateCloudEventExtension: tc.validateCloudEventExtension,
				})
			} else {
				grpcServer = testinggrpc.StartTestAppCallbackGRPCServer(t, port, &channelt.MockServer{
					Error:                       tc.responseError,
					ValidateCloudEventExtension: tc.validateCloudEventExtension,
				})
			}
			if grpcServer != nil {
				// properly stop the gRPC server
				defer grpcServer.Stop()
			}

			grpc := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
			ps.channels = channels.New(channels.Options{
				Registry:            reg,
				ComponentStore:      compstore.New(),
				GlobalConfig:        new(config.Configuration),
				AppConnectionConfig: config.AppConnectionConfig{Port: port},
				GRPC:                grpc,
			})
			require.NoError(t, ps.channels.Refresh())
			ps.grpc = grpc

			// act
			err = ps.publishMessageGRPC(context.Background(), tc.message)

			// assert
			if tc.expectedError != nil {
				assert.Equal(t, err.Error(), tc.expectedError.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestTracingOnNewPublishedMessage(t *testing.T) {
	t.Run("succeeded to publish message with TraceParent in metadata", func(t *testing.T) {
		comp := &mockSubscribePubSub{}
		require.NoError(t, comp.Init(context.Background(), contribpubsub.Metadata{}))

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
				IsHTTP:     true,
				Channels:   new(channels.Channels).WithAppChannel(mockAppChannel),
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
			err = comp.Publish(context.TODO(), &contribpubsub.PublishRequest{
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
