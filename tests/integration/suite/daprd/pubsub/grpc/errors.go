/*
Copyright 2023 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"fmt"
	apiErrors "github.com/dapr/dapr/pkg/api/errors"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	kitErrors "github.com/dapr/kit/errors"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"testing"
)

func init() {
	suite.Register(new(standardizedErrors))
}

type standardizedErrors struct {
	daprd *daprd.Daprd
}

func (e *standardizedErrors) Setup(t *testing.T) []framework.Option {
	//spin up a new daprd with a pubsub component
	e.daprd = procdaprd.New(t, procdaprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypubsub
spec:
  type: pubsub.in-memory
  version: v1
`,
	))

	return []framework.Option{
		framework.WithProcesses(e.daprd),
	}
}

func (e *standardizedErrors) Run(t *testing.T, ctx context.Context) {

	e.daprd.WaitUntilRunning(t, ctx)

	conn, connErr := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", e.daprd.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, connErr)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Covers apiErrors.PubSubNotFound()
	t.Run("pubsub doesn't exist", func(t *testing.T) {
		req := &rtv1.PublishEventRequest{
			PubsubName: "pubsub-doesn't-exist",
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("pubsub %s not found", "pubsub-doesn't-exist"), s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixPubSub+kitErrors.CodeNotFound, errInfo.GetReason())
		require.Equal(t, kitErrors.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())
	})

	// Covers apiErrors.PubSubNotConfigured() -> TODO: force adapter interface not being there
	t.Run("pubsub not configured", func(t *testing.T) {
	})

	// Covers apiErrors.PubSubNameEmpty()
	t.Run("pubsub name empty", func(t *testing.T) {
		req := &rtv1.PublishEventRequest{
			PubsubName: "",
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "pubsub name is empty", s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixPubSub+"NAME_EMPTY", errInfo.GetReason())
		require.Equal(t, kitErrors.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())
	})

	// Covers apiErrors.PubSubTopicEmpty()
	t.Run("pubsub topic empty", func(t *testing.T) {
		req := &rtv1.PublishEventRequest{
			PubsubName: "mypubsub",
			Topic:      "",
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("topic is empty in pubsub mypubsub"), s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixPubSub+"TOPIC"+apiErrors.PostFixNameEmpty, errInfo.GetReason())
		require.Equal(t, kitErrors.Domain, errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())
	})

	// Covers apiErrors.PubSubMetadataDeserialize()
	t.Run("pubsub metadata deserialization", func(t *testing.T) {
		metadata := map[string]string{"rawPayload": "invalidBooleanValue"}
		req := &rtv1.PublishEventRequest{
			PubsubName: "mypubsub",
			Topic:      "topic",
			Metadata:   metadata,
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("failed deserializing metadata: %v", metadata), s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixPubSub+"METADATA_DESERIALIZATION", errInfo.GetReason())
		require.Equal(t, kitErrors.Domain, errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())
	})

	// Covers apiErrors.PubSubPublishMessage() -> TODO: need publish func for pluggable pubsub
	t.Run("pubsub publish message issue", func(t *testing.T) {
	})

	// Covers apiErrors.PubSubCloudEventCreation()
	t.Run("pubsub cloud event creation issue", func(t *testing.T) {
		metadata := map[string]string{"rawPayload": "false"}
		invalidData := []byte(`{"missing_quote: invalid}`)

		req := &rtv1.PublishEventRequest{
			PubsubName:      "mypubsub",
			Topic:           "topic",
			Metadata:        metadata,
			Data:            invalidData,
			DataContentType: "application/cloudevents+json",
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, "cannot create cloudevent", s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixPubSub+"CLOUD_EVENT_CREATION", errInfo.GetReason())
		require.Equal(t, kitErrors.Domain, errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())
	})

	// Covers apiErrors.PubSubMarshalEnvelope(): need pluggable pubsub
	t.Run("pubsub marshal envelope issue", func(t *testing.T) {

	})

	// Covers apiErrors.PubSubPublishForbidden() -> requires scoping, so need pluggable
	t.Run("pubsub publish forbidden", func(t *testing.T) {

	})

	// Covers apiErrors.PubSubTestNotFound() -> need pluggable
	t.Run("pubsub test not found", func(t *testing.T) {

	})

	// Covers apiErrors.PubSubOubox() -> need pluggable
	t.Run("pubsub cloud outbox issue", func(t *testing.T) {

	})

	// Covers apiErrors.PubSubMarshalEvents()
	t.Run("pubsub marshal events issue", func(t *testing.T) {
		name := "mypubsub"
		topic := "topic"

		req := &rtv1.BulkPublishRequest{
			PubsubName: name,
			Topic:      topic,
			Entries: []*rtv1.BulkPublishRequestEntry{
				{
					EntryId:     "",
					Event:       nil,
					ContentType: "",
					Metadata:    nil},
			},
			Metadata: nil,
		}
		_, err := client.BulkPublishEventAlpha1(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("error marshaling events to bytes for topic %s pubsub %s. error: entryId is duplicated or not present for entry", topic, name), s.Message())

		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kitErrors.CodePrefixPubSub+"MARSHAL_EVENTS", errInfo.GetReason())
		require.Equal(t, kitErrors.Domain, errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())
	})

}
