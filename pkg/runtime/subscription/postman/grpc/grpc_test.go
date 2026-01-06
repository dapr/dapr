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

package grpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/phayes/freeport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	googlegrpc "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	contribpubsub "github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/pkg/api/grpc/manager"
	channelt "github.com/dapr/dapr/pkg/channel/testing"
	"github.com/dapr/dapr/pkg/config"
	"github.com/dapr/dapr/pkg/modes"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	rterrors "github.com/dapr/dapr/pkg/runtime/errors"
	"github.com/dapr/dapr/pkg/runtime/pubsub"
	testinggrpc "github.com/dapr/dapr/pkg/testing/grpc"
	"github.com/dapr/kit/ptr"
)

func TestErrorPublishedNonCloudEvent(t *testing.T) {
	t.Parallel()

	topic := "topic1"

	testPubSubMessage := &pubsub.SubscribedMessage{
		CloudEvent: map[string]interface{}{},
		Topic:      topic,
		Data:       []byte("testing"),
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
	}

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
			t.Parallel()

			mockClientConn := channelt.MockClientConn{
				InvokeFn: func(ctx context.Context, method string, args interface{}, reply interface{}, opts ...googlegrpc.CallOption) error {
					if tc.Error != nil {
						return tc.Error
					}

					response, ok := reply.(*runtimev1pb.TopicEventResponse)
					require.True(t, ok, "expected TopicEventResponse type")

					response.Status = tc.Status

					return nil
				},
			}

			channel := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{})
			channel.SetAppClientConn(&mockClientConn)
			g := New(Options{
				Channel: channel,
			})

			err := g.Deliver(t.Context(), testPubSubMessage)
			assert.Equal(t, tc.ExpectError, err != nil, "expected error: %v, got: %v", tc.ExpectError, err)
		})
	}
}

func TestOnNewPublishedMessage(t *testing.T) {
	t.Parallel()

	topic := "topic1"

	envelope := contribpubsub.NewCloudEventsEnvelope("", "", contribpubsub.DefaultCloudEventType, "", topic,
		"testpubsub2", "", []byte("Test Message"), "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01", "")
	// add custom attributes
	envelope["customInt"] = 123
	envelope["customString"] = "abc"
	envelope["customBool"] = true
	envelope["customFloat"] = 1.23
	envelope["customArray"] = []interface{}{"a", "b", 789, 3.1415}
	envelope["customMap"] = map[string]interface{}{"a": "b", "c": 456}
	b, err := json.Marshal(envelope)
	require.NoError(t, err)

	testPubSubMessage := &pubsub.SubscribedMessage{
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

	testPubSubMessageBase64 := &pubsub.SubscribedMessage{
		CloudEvent: envelope,
		Topic:      topic,
		Data:       base64,
		Metadata:   map[string]string{"pubsubName": "testpubsub"},
		Path:       "topic1",
	}

	testCases := []struct {
		name                        string
		message                     *pubsub.SubscribedMessage
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
			expectedError:  pubsub.ErrMessageDropped,
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
		{
			name:           "succeeded to publish message to user app with traceparent",
			message:        testPubSubMessage,
			responseStatus: runtimev1pb.TopicEventResponse_SUCCESS,
			validateCloudEventExtension: ptr.Of(map[string]interface{}{
				"customInt":    float64(123),
				"customString": "abc",
				"customBool":   true,
				"customFloat":  float64(1.23),
				"customArray":  []interface{}{"a", "b", float64(789), float64(3.1415)},
				"customMap":    map[string]interface{}{"a": "b", "c": float64(456)},
				"traceparent":  "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01",
			}),
		},
		{
			name:             "fail to publish message to user app with different traceparent",
			message:          testPubSubMessage,
			noResponseStatus: true,
			responseError:    assert.AnError,
			expectedError: fmt.Errorf(
				"error returned from app while processing pub/sub event %v: %w",
				testPubSubMessage.CloudEvent[contribpubsub.IDField],
				rterrors.NewRetriable(status.Error(codes.Unknown, "cloud event extension traceparent with value 00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-01 is not valid")),
			),
			validateCloudEventExtension: ptr.Of(map[string]interface{}{
				"customInt":    float64(123),
				"customString": "abc",
				"customBool":   true,
				"customFloat":  float64(1.23),
				"customArray":  []interface{}{"a", "b", float64(789), float64(3.1415)},
				"customMap":    map[string]interface{}{"a": "b", "c": float64(456)},
				"traceparent":  "00-c24c2deeb837b9b5e7101a1235b479c5-6784475fca41cdff-03",
			}),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			port, err := freeport.GetFreePort()
			require.NoError(t, err)

			var grpcServer *googlegrpc.Server

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

			channel := manager.NewManager(nil, modes.StandaloneMode, &manager.AppChannelConfig{Port: port})
			g := New(Options{
				Channel: channel,
				Tracing: &config.TracingSpec{
					SamplingRate: "100",
					Stdout:       false,
					Zipkin:       nil,
					Otel:         nil,
				},
			})

			err = g.Deliver(t.Context(), tc.message)
			if tc.expectedError != nil {
				assert.Equal(t, err.Error(), tc.expectedError.Error())
			} else {
				require.NoError(t, err)
			}
		})
	}
}
