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

package baggage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/client"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	grpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	httpapp "github.com/dapr/dapr/tests/integration/framework/process/http/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(pubsubHTTP))
	suite.Register(new(pubsubGRPC))
}

// pubsubHTTP verifies that tracestate and baggage headers set on a publish
// request are restored on the HTTP subscriber's delivery request.
type pubsubHTTP struct {
	daprd    *daprd.Daprd
	headerCh chan nethttp.Header
}

func (p *pubsubHTTP) Setup(t *testing.T) []framework.Option {
	p.headerCh = make(chan nethttp.Header, 1)

	app := httpapp.New(t,
		httpapp.WithHandlerFunc("/test-topic", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			p.headerCh <- r.Header.Clone()
			w.WriteHeader(nethttp.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "SUCCESS"})
		}),
		httpapp.WithHandlerFunc("/dapr/subscribe", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			json.NewEncoder(w).Encode([]map[string]any{
				{"pubsubname": "mypub", "topic": "test-topic", "route": "/test-topic"},
			})
		}),
	)

	p.daprd = daprd.New(t,
		daprd.WithAppPort(app.Port()),
		daprd.WithAppProtocol("http"),
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
		framework.WithProcesses(app, p.daprd),
	}
}

func (p *pubsubHTTP) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)
	httpClient := client.HTTP(t)

	pubURL := fmt.Sprintf("http://localhost:%d/v1.0/publish/mypub/test-topic", p.daprd.HTTPPort())
	req, err := nethttp.NewRequestWithContext(ctx, nethttp.MethodPost, pubURL, bytes.NewReader([]byte(`{"message": "hello"}`)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02")
	req.Header.Set("tracestate", "vendor=value")
	req.Header.Set("baggage", "key1=value1,key2=value2")

	resp, err := httpClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, nethttp.StatusNoContent, resp.StatusCode)
	resp.Body.Close()

	select {
	case headers := <-p.headerCh:
		assert.Equal(t, "vendor=value", headers.Get("tracestate"))
		assert.Equal(t, "key1=value1,key2=value2", headers.Get("baggage"))
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timed out waiting for pubsub event to be delivered to app")
	}
}

// pubsubGRPC verifies that tracestate and baggage metadata set on a publish
// request are restored on the gRPC subscriber's delivery metadata.
type pubsubGRPC struct {
	daprd *daprd.Daprd
	ch    chan metadata.MD
}

func (p *pubsubGRPC) Setup(t *testing.T) []framework.Option {
	p.ch = make(chan metadata.MD, 1)

	app := grpcapp.New(t,
		grpcapp.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			if !ok {
				md = metadata.MD{}
			}
			p.ch <- md
			return &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS}, nil
		}),
		grpcapp.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "test-topic",
						Routes:     &rtv1.TopicRoutes{Default: "/test-topic"},
					},
				},
			}, nil
		}),
	)

	p.daprd = daprd.New(t,
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
		framework.WithProcesses(app, p.daprd),
	}
}

func (p *pubsubGRPC) Run(t *testing.T, ctx context.Context) {
	p.daprd.WaitUntilRunning(t, ctx)
	grpcClient := p.daprd.GRPCClient(t, ctx)

	pubCtx := metadata.AppendToOutgoingContext(ctx,
		"traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-02",
		"tracestate", "vendor=value",
		"baggage", "key1=value1,key2=value2",
	)

	_, err := grpcClient.PublishEvent(pubCtx, &rtv1.PublishEventRequest{
		PubsubName:      "mypub",
		Topic:           "test-topic",
		Data:            []byte(`{"message": "hello"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	select {
	case md := <-p.ch:
		tracestate := md.Get("tracestate")
		require.NotEmpty(t, tracestate)
		assert.Equal(t, "vendor=value", tracestate[0])

		baggageVal := md.Get("baggage")
		require.NotEmpty(t, baggageVal)
		assert.Equal(t, "key1=value1,key2=value2", baggageVal[0])
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timed out waiting for pubsub event to be delivered to app")
	}
}
