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

package match

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t, subscriber.WithRoutes(
		"/aaa", "/type", "/foo", "/bar", "/topic",
		"/123", "/456", "/order7def", "/order7rule",
	))

	h.daprd = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: mypub
spec:
 type: pubsub.in-memory
 version: v1
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: type
spec:
 pubsubname: mypub
 topic: type
 routes:
  default: /aaa
  rules:
  - path: /type
    match: event.type == "com.dapr.event.sent"
  - path: /foo
    match: ""
  - path: /bar
    match: event.type == "com.dapr.event.recv"
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: order1
spec:
 pubsubname: mypub
 topic: order1
 routes:
  default: /aaa
  rules:
  - path: /type
    match: event.type == "com.dapr.event.sent"
  - path: /topic
    match: event.topic == "order1"
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: order2
spec:
 pubsubname: mypub
 topic: order2
 routes:
  default: /aaa
  rules:
  - path: /topic
    match: event.topic == "order2"
  - path: /type
    match: event.type == "com.dapr.event.sent"
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: order3
spec:
 pubsubname: mypub
 topic: order3
 routes:
  default: /aaa
  rules:
  - path: /123
    match: event.topic == "order3"
  - path: /456
    match: event.topic == "order3"
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: order4
spec:
 pubsubname: mypub
 topic: order4
 routes:
  rules:
  - path: /123
    match: event.topic == "order5"
  - path: /456
    match: event.topic == "order6"
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: order7
spec:
 pubsubname: mypub
 topic: order7
 default: /order7def
 routes:
  rules:
  - path: /order7rule
    match: ""
`))

	return []framework.Option{
		framework.WithProcesses(h.sub, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)
	client := h.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "type",
	})
	require.NoError(t, err)
	resp := h.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.Route)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order1",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/type", resp.Route)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order2",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/topic", resp.Route)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order3",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.Route)

	h.sub.ExpectPublishNoReceive(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "order4",
	})

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "order7",
	})
	require.NoError(t, err)
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/order7rule", resp.Route)
}
