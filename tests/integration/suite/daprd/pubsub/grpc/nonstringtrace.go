/*
Copyright 2026 The Dapr Authors
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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(nonstringtrace))
}

// nonstringtrace verifies that a published CloudEvent whose trace fields
// (traceid/traceparent) are not strings is delivered over gRPC without crashing
// daprd. The gRPC postman already guards its trace-field assertion with a
// checked conversion; this locks that behaviour in, mirroring the HTTP case.
type nonstringtrace struct {
	daprd     *daprd.Daprd
	deliverCh chan struct{}
}

func (n *nonstringtrace) Setup(t *testing.T) []framework.Option {
	n.deliverCh = make(chan struct{}, 1)

	app := app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			select {
			case n.deliverCh <- struct{}{}:
			default:
			}
			return &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS}, nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{{
					PubsubName: "mypub",
					Topic:      "test-topic",
					Routes:     &rtv1.TopicRoutes{Default: "/test-topic"},
				}},
			}, nil
		}),
	)

	n.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(app, n.daprd),
	}
}

func (n *nonstringtrace) Run(t *testing.T, ctx context.Context) {
	n.daprd.WaitUntilRunning(t, ctx)
	client := n.daprd.GRPCClient(t, ctx)

	for name, payload := range map[string]string{
		"numeric traceid":     `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceid":12345}`,
		"numeric traceparent": `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceparent":12345}`,
		"object traceparent":  `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceparent":{"x":1}}`,
		"bool traceid":        `{"specversion":"1.0","id":"a","source":"b","type":"c","datacontenttype":"text/plain","data":"hello","traceid":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
				PubsubName:      "mypub",
				Topic:           "test-topic",
				Data:            []byte(payload),
				DataContentType: "application/cloudevents+json",
			})
			require.NoError(t, err)

			select {
			case <-n.deliverCh:
			case <-time.After(10 * time.Second):
				assert.Fail(t, "timed out waiting for pubsub delivery; daprd may have crashed")
			}

			n.daprd.WaitUntilRunning(t, ctx)
		})
	}
}
