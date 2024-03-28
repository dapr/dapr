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
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub"
	inmemory "github.com/dapr/dapr/tests/integration/framework/process/pubsub/in-memory"
	"github.com/dapr/dapr/tests/integration/framework/socket"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(bulk))
}

type bulk struct {
	daprd *daprd.Daprd
	app   *subscriber.Subscriber
	pmrCh chan *compv1pb.PullMessagesResponse
}

func (b *bulk) Setup(t *testing.T) []framework.Option {
	b.pmrCh = make(chan *compv1pb.PullMessagesResponse)
	b.app = subscriber.New(t, subscriber.WithBulkRoutes("/abc"))

	socket := socket.New(t)

	inmem := pubsub.New(t,
		pubsub.WithSocket(socket),
		pubsub.WithPullMessagesChannel(b.pmrCh),
		pubsub.WithPubSub(inmemory.NewWrappedInMemory(t)),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.app.Port()),
		daprd.WithSocket(t, socket),
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: foo
spec:
 type: pubsub.%s
 version: v1
 metadata:
 - name: host
   value: "localhost:6650"
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: foo
 topic: bar
 route: /abc
 bulkSubscribe:
  enabled: true
  maxMessagesCount: 100
  maxAwaitDurationMs: 40
`, inmem.SocketName())),
	)

	return []framework.Option{
		framework.WithProcesses(inmem, b.app, b.daprd),
	}
}

func (b *bulk) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	client := b.daprd.GRPCClient(t, ctx)
	meta, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	require.Len(t, meta.GetSubscriptions(), 1)

	b.pmrCh <- &compv1pb.PullMessagesResponse{
		Data:      []byte(`{"data":"helloworld","datacontenttype":"text/plain","id":"b959cd5a-29e5-42ca-89e2-c66f4402f273","pubsubname":"foo","source":"foo","specversion":"1.0","time":"2024-03-27T23:47:53Z","topic":"bar","traceid":"00-00000000000000000000000000000000-0000000000000000-00","traceparent":"00-00000000000000000000000000000000-0000000000000000-00","tracestate":"","type":"com.dapr.event.sent"}`),
		TopicName: "bar",
		Id:        "foo",
		Metadata:  map[string]string{"content-length": "123"},
	}

	b.app.ReceiveBulk(t, ctx)
}
