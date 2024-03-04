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

package mixed

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
	suite.Register(new(match))
}

type match struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (m *match) Setup(t *testing.T) []framework.Option {
	m.sub = subscriber.New(t, subscriber.WithRoutes("/a/b/c/d", "/123"))
	m.daprd = daprd.New(t,
		daprd.WithAppPort(m.sub.Port()),
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
 name: sub1
spec:
 pubsubname: mypub
 topic: a
 route: /a/b/c/d
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub2
spec:
 pubsubname: mypub
 topic: a
 routes:
  rules:
  - path: /123
    match: event.topic == "a"
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: sub3
spec:
 pubsubname: mypub
 topic: b
 routes:
  rules:
  - path: /123
    match: event.topic == "b"
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: sub4
spec:
 pubsubname: mypub
 topic: b
 route: /a/b/c/d
`))

	return []framework.Option{
		framework.WithProcesses(m.sub, m.daprd),
	}
}

func (m *match) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	m.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      m.daprd,
		PubSubName: "mypub",
		Topic:      "a",
	})
	assert.Equal(t, "/a/b/c/d", m.sub.Receive(t, ctx).Route)

	m.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      m.daprd,
		PubSubName: "mypub",
		Topic:      "b",
	})
	assert.Equal(t, "/123", m.sub.Receive(t, ctx).Route)
}
