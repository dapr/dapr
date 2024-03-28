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

package emptymatch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.sub = subscriber.New(t)

	g.daprd = daprd.New(t,
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
 name: justpath
spec:
 pubsubname: mypub
 topic: justpath
 routes:
  rules:
  - path: /justpath
    match: ""
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: defaultandpath
spec:
 pubsubname: mypub
 topic: defaultandpath
 routes:
  default: /abc
  rules:
  - path: /123
    match: ""
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: multipaths
spec:
 pubsubname: mypub
 topic: multipaths
 routes:
  rules:
  - path: /xyz
    match: ""
  - path: /456
    match: ""
  - path: /789
    match: ""
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: defaultandpaths
spec:
 pubsubname: mypub
 topic: defaultandpaths
 routes:
  default: /def
  rules:
  - path: /zyz
    match: ""
  - path: /aaa
    match: ""
  - path: /bbb
    match: ""
`))

	return []framework.Option{
		framework.WithProcesses(g.sub, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)
	client := g.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "justpath",
	})
	require.NoError(t, err)
	resp := g.sub.Receive(t, ctx)
	assert.Equal(t, "/justpath", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "defaultandpath",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "multipaths",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/xyz", resp.GetPath())

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub",
		Topic:      "defaultandpaths",
	})
	require.NoError(t, err)
	resp = g.sub.Receive(t, ctx)
	assert.Equal(t, "/zyz", resp.GetPath())
}
