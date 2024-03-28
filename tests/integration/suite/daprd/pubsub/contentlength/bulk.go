/*
Copyright 2024 The Dapr Authors
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

package contentlength

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	compv1pb "github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	grpcsub "github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	httpsub "github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulk))
}

type bulk struct {
	daprdhttp *daprd.Daprd
	daprdgrpc *daprd.Daprd
	apphttp   *httpsub.Subscriber
	appgrpc   *grpcsub.Subscriber
	pmrChHTTP chan *compv1pb.PullMessagesResponse
	pmrChGRPC chan *compv1pb.PullMessagesResponse
}

func (b *bulk) Setup(t *testing.T) []framework.Option {
	b.pmrChHTTP = make(chan *compv1pb.PullMessagesResponse)
	b.pmrChGRPC = make(chan *compv1pb.PullMessagesResponse)
	b.apphttp = httpsub.New(t, httpsub.WithBulkRoutes("/abc"))
	b.appgrpc = grpcsub.New(t)

	socket1 := socket.New(t)
	inmem1 := pubsub.New(t,
		pubsub.WithSocket(socket1),
		pubsub.WithPullMessagesChannel(b.pmrChHTTP),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t)),
	)

	b.daprdhttp = daprd.New(t,
		daprd.WithAppPort(b.apphttp.Port()),
		daprd.WithSocket(t, socket1),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: foo
spec:
 type: pubsub.%s
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: mysub1
spec:
 topic: bar1
 route: /abc
 pubsubname: foo
 bulkSubscribe:
  enabled: true
`, inmem1.SocketName())),
	)

	socket2 := socket.New(t)
	inmem2 := pubsub.New(t,
		pubsub.WithSocket(socket2),
		pubsub.WithPullMessagesChannel(b.pmrChGRPC),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t)),
	)

	b.daprdgrpc = daprd.New(t,
		daprd.WithAppPort(b.appgrpc.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithSocket(t, socket2),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: foo
spec:
 type: pubsub.%s
 version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: mysub1
spec:
 topic: bar2
 route: /abc
 pubsubname: foo
 bulkSubscribe:
  enabled: true
`, inmem2.SocketName())),
	)

	return []framework.Option{
		framework.WithProcesses(inmem1, inmem2, b.daprdhttp, b.daprdgrpc, b.apphttp, b.appgrpc),
	}
}

func (b *bulk) Run(t *testing.T, ctx context.Context) {
	b.daprdhttp.WaitUntilRunning(t, ctx)
	b.daprdgrpc.WaitUntilRunning(t, ctx)

	clientHTTP := b.daprdhttp.GRPCClient(t, ctx)
	meta, err := clientHTTP.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	require.Len(t, meta.GetSubscriptions(), 1)
	b.pmrChHTTP <- &compv1pb.PullMessagesResponse{
		Data:      []byte(`{"data":"helloworld","datacontenttype":"text/plain","id":"b959cd5a-29e5-42ca-89e2-c66f4402f273","pubsubname":"foo","source":"foo","specversion":"1.0","time":"2024-03-27T23:47:53Z","topic":"bar1","traceid":"00-00000000000000000000000000000000-0000000000000000-00","traceparent":"00-00000000000000000000000000000000-0000000000000000-00","tracestate":"","type":"com.dapr.event.sent"}`),
		TopicName: "bar",
		Id:        "foo",
		Metadata:  map[string]string{"content-length": "123"},
	}
	b.apphttp.ReceiveBulk(t, ctx)

	clientGRPC := b.daprdgrpc.GRPCClient(t, ctx)
	meta, err = clientGRPC.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	require.Len(t, meta.GetSubscriptions(), 1)
	b.pmrChGRPC <- &compv1pb.PullMessagesResponse{
		Data:      []byte(`{"data":"helloworld","datacontenttype":"text/plain","id":"b959cd5a-29e5-42ca-89e2-c66f4402f273","pubsubname":"foo","source":"foo","specversion":"1.0","time":"2024-03-27T23:47:53Z","topic":"bar2","traceid":"00-00000000000000000000000000000000-0000000000000000-00","traceparent":"00-00000000000000000000000000000000-0000000000000000-00","tracestate":"","type":"com.dapr.event.sent"}`),
		TopicName: "bar",
		Id:        "foo",
		Metadata:  map[string]string{"content-length": "123"},
	}
	b.appgrpc.ReceiveBulk(t, ctx)
}
