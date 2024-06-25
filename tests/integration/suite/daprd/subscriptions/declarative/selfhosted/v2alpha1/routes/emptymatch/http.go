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
		"/justpath", "/abc", "/123", "/def", "/zyz",
		"/aaa", "/bbb", "/xyz", "/456", "/789",
	))

	h.daprd = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
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
		framework.WithProcesses(h.sub, h.daprd),
	}
}

func (h *http) Run(t *testing.T, ctx context.Context) {
	h.daprd.WaitUntilRunning(t, ctx)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "justpath",
	})
	resp := h.sub.Receive(t, ctx)
	assert.Equal(t, "/justpath", resp.Route)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "defaultandpath",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.Route)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "multipaths",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/xyz", resp.Route)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "defaultandpaths",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/zyz", resp.Route)
}
