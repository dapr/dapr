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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"golang.org/x/net/nettest"

	componentspubsub "github.com/dapr/components-contrib/pubsub"
	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/util"

	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc"
	grpcCodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	apierrors "github.com/dapr/dapr/pkg/api/errors"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/suite"
	kiterrors "github.com/dapr/kit/errors"
)

func init() {
	suite.Register(new(standardizedErrors))
}

type standardizedErrors struct {
	daprd *daprd.Daprd
}

func (e *standardizedErrors) Setup(t *testing.T) []framework.Option {
	if runtime.GOOS == "windows" {
		t.Skip("skipping unix socket based test on windows")
	}

	// Darwin enforces a maximum 104 byte socket name limit, so we need to be a
	// bit fancy on how we generate the name.
	tmp, err := nettest.LocalPath()
	require.NoError(t, err)

	socketDir := filepath.Join(tmp, util.RandomString(t, 4))
	require.NoError(t, os.MkdirAll(socketDir, 0o700))
	t.Cleanup(func() {
		require.NoError(t, os.RemoveAll(socketDir))
	})

	pubsubInMem := pubsub.New(t,
		pubsub.WithSocketDirectory(socketDir),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t,
			inmemory.WithFeatures(),
			inmemory.WithPublishFn(func(ctx context.Context, req *componentspubsub.PublishRequest) error {
				return errors.New("outbox error")
			}),
		)),
	)

	// spin up a new daprd with a pubsub component
	e.daprd = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypubsub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: pubsub-outbox
spec:
  type: pubsub.%s
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: state-outbox
spec:
  type: state.in-memory
  version: v1
  metadata:
  - name: outboxPublishPubsub
    value: "pubsub-outbox"
  - name: outboxPublishTopic
    value: "goodTopic" # force topic mismatch error
  - name: outboxPubsub
    value: "pubsub-outbox"
  - name: outboxDiscardWhenMissingState #Optional. Defaults to false
    value: false
`, pubsubInMem.SocketName())),
		daprd.WithExecOptions(exec.WithEnvVars(t,
			"DAPR_COMPONENTS_SOCKETS_FOLDER", socketDir,
		)),
	)

	return []framework.Option{
		framework.WithProcesses(pubsubInMem, e.daprd),
	}
}

func (e *standardizedErrors) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	conn, connErr := grpc.DialContext(ctx, fmt.Sprintf("localhost:%d", e.daprd.GRPCPort()), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	require.NoError(t, connErr)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := rtv1.NewDaprClient(conn)

	// Covers apierrors.PubSubNotFound()
	t.Run("pubsub doesn't exist", func(t *testing.T) {
		name := "pubsub-doesn't-exist"
		req := &rtv1.PublishEventRequest{
			PubsubName: name,
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)
		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		require.Equal(t, fmt.Sprintf("pubsub %s is not found", "pubsub-doesn't-exist"), s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kiterrors.CodePrefixPubSub+kiterrors.CodeNotFound, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
	})

	// Covers apierrors.PubSubNameEmpty()
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
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kiterrors.CodePrefixPubSub+apierrors.PostFixNameEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "pubsub", resInfo.GetResourceType())
		require.Equal(t, "", resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
	})

	// Covers apierrors.PubSubTopicEmpty()
	t.Run("pubsub topic empty", func(t *testing.T) {
		name := "mypubsub"
		req := &rtv1.PublishEventRequest{
			PubsubName: name,
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
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kiterrors.CodePrefixPubSub+"TOPIC"+apierrors.PostFixNameEmpty, errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.Nil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "pubsub", resInfo.GetResourceType())
		require.Equal(t, name, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
	})

	// Covers apierrors.PubSubMetadataDeserialize()
	t.Run("pubsub metadata deserialization", func(t *testing.T) {
		name := "mypubsub"
		metadata := map[string]string{"rawPayload": "invalidBooleanValue"}
		req := &rtv1.PublishEventRequest{
			PubsubName: name,
			Topic:      "topic",
			Metadata:   metadata,
		}
		_, err := client.PublishEvent(ctx, req)

		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.InvalidArgument, s.Code())
		expectedErr := "rawPayload value must be a valid boolean: actual is 'invalidBooleanValue'"
		require.Equal(t, fmt.Sprintf("failed deserializing metadata. Error: %s", expectedErr), s.Message())
		// Check status details
		require.Len(t, s.Details(), 2)

		var errInfo *errdetails.ErrorInfo
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kiterrors.CodePrefixPubSub+"METADATA_DESERIALIZATION", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "pubsub", resInfo.GetResourceType())
		require.Equal(t, name, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
	})

	// Covers apierrors.PubSubCloudEventCreation()
	t.Run("pubsub cloud event creation issue", func(t *testing.T) {
		name := "mypubsub"
		metadata := map[string]string{"rawPayload": "false"}
		invalidData := []byte(`{"missing_quote: invalid}`)

		req := &rtv1.PublishEventRequest{
			PubsubName:      name,
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
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kiterrors.CodePrefixPubSub+"CLOUD_EVENT_CREATION", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "pubsub", resInfo.GetResourceType())
		require.Equal(t, name, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
	})

	// Covers apierrors.PubSubMarshalEvents()
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
					Metadata:    nil,
				},
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
		var resInfo *errdetails.ResourceInfo

		for _, detail := range s.Details() {
			switch d := detail.(type) {
			case *errdetails.ErrorInfo:
				errInfo = d
			case *errdetails.ResourceInfo:
				resInfo = d
			default:
				require.FailNow(t, "unexpected status detail")
			}
		}
		require.NotNil(t, errInfo, "ErrorInfo should be present")
		require.Equal(t, kiterrors.CodePrefixPubSub+"MARSHAL_EVENTS", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())

		require.NotNil(t, resInfo, "ResourceInfo should be present")
		require.Equal(t, "pubsub", resInfo.GetResourceType())
		require.Equal(t, name, resInfo.GetResourceName())
		require.Empty(t, resInfo.GetOwner())
	})

	t.Run("pubsub outbox", func(t *testing.T) {
		name := "state-outbox"
		ops := make([]*rtv1.TransactionalStateOperation, 0)
		ops = append(ops, &rtv1.TransactionalStateOperation{
			OperationType: "upsert",
			Request: &commonv1.StateItem{
				Key:   "key1",
				Value: []byte("val1"),
			},
		},
			&rtv1.TransactionalStateOperation{
				OperationType: "delete",
				Request: &commonv1.StateItem{
					Key: "key2",
				},
			})
		req := &rtv1.ExecuteStateTransactionRequest{
			StoreName:  name,
			Operations: ops,
		}
		_, err := client.ExecuteStateTransaction(ctx, req)
		require.Error(t, err)

		s, ok := status.FromError(err)
		require.True(t, ok)
		require.Equal(t, grpcCodes.Internal, s.Code())
		expectedErrMsg := "error while publishing outbox message: rpc error: code = Unknown desc = outbox error"
		require.Equal(t, expectedErrMsg, s.Message())

		// Check status details
		require.Len(t, s.Details(), 1)

		var errInfo *errdetails.ErrorInfo
		errInfo, ok = s.Details()[0].(*errdetails.ErrorInfo)

		require.True(t, ok)
		require.Equal(t, kiterrors.CodePrefixPubSub+"OUTBOX", errInfo.GetReason())
		require.Equal(t, "dapr.io", errInfo.GetDomain())
		require.NotNil(t, errInfo.GetMetadata())
	})
}
