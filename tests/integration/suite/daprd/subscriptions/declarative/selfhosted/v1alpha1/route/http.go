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

package route

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
	h.sub = subscriber.New(t, subscriber.WithRoutes("/a", "/d/c/b/a", "/a/b/c/d"))

	h.daprd = daprd.New(t,
		daprd.WithAppPort(h.sub.Port()),
		daprd.WithAppProtocol("http"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub1
spec:
  pubsubname: mypub
  topic: a
  route: /a/b/c/d
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub2
spec:
  pubsubname: mypub
  topic: a
  route: /a
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub3
spec:
  pubsubname: mypub
  topic: b
  route: /a
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub5
spec:
  pubsubname: mypub
  topic: b
  route: /d/c/b/a
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
  name: mysub6
spec:
  pubsubname: mypub
  topic: b
  route: /a/b/c/d
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
		Topic:      "a",
	})
	resp := h.sub.Receive(t, ctx)
	assert.Equal(t, "/a/b/c/d", resp.Route)
	assert.Empty(t, resp.Data())

	// Uses the first defined route for a topic when two declared routes match
	// the topic.
	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "b",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Empty(t, resp.Data())
}
