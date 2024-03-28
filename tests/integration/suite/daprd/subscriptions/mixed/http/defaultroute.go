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

package http

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
	suite.Register(new(defaultroute))
}

type defaultroute struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (d *defaultroute) Setup(t *testing.T) []framework.Option {
	d.sub = subscriber.New(t,
		subscriber.WithRoutes("/123", "/456", "/zyx", "/xyz"),
		subscriber.WithProgrammaticSubscriptions(
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "a",
				Route:      "/123",
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "b",
				Routes: subscriber.RoutesJSON{
					Default: "/xyz",
				},
			},
		),
	)

	d.daprd = daprd.New(t,
		daprd.WithAppPort(d.sub.Port()),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
---
apiVersion: dapr.io/v2alpha1
Kind: Subscription
metadata:
 name: sub1
spec:
 pubsubname: mypub
 topic: a
 routes:
  default: /456
---
apiVersion: dapr.io/v1alpha1
Kind: Subscription
metadata:
 name: sub2
spec:
 pubsubname: mypub
 topic: b
 route: /zyx
`))

	return []framework.Option{
		framework.WithProcesses(d.sub, d.daprd),
	}
}

func (d *defaultroute) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	d.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      d.daprd,
		PubSubName: "mypub",
		Topic:      "a",
	})
	assert.Equal(t, "/123", d.sub.Receive(t, ctx).Route)

	d.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      d.daprd,
		PubSubName: "mypub",
		Topic:      "b",
	})
	assert.Equal(t, "/xyz", d.sub.Receive(t, ctx).Route)
}
