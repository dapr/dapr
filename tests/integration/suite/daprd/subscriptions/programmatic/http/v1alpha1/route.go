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

package v1alpha1

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
	suite.Register(new(route))
}

type route struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (r *route) Setup(t *testing.T) []framework.Option {
	r.sub = subscriber.New(t,
		subscriber.WithRoutes("/a", "/a/b/c/d", "/d/c/b/a"),
		subscriber.WithProgrammaticSubscriptions(
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "a",
				Route:      "/a/b/c/d",
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "a",
				Route:      "/a",
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "b",
				Route:      "/a",
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "b",
				Route:      "/d/c/b/a",
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "b",
				Route:      "/a/b/c/d",
			},
		),
	)

	r.daprd = daprd.New(t,
		daprd.WithAppPort(r.sub.Port()),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(r.sub, r.daprd),
	}
}

func (r *route) Run(t *testing.T, ctx context.Context) {
	r.daprd.WaitUntilRunning(t, ctx)

	r.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      r.daprd,
		PubSubName: "mypub",
		Topic:      "a",
	})
	resp := r.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Empty(t, resp.Data())

	// Uses the first defined route for a topic when two declared routes match
	// the topic.
	r.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      r.daprd,
		PubSubName: "mypub",
		Topic:      "b",
	})
	resp = r.sub.Receive(t, ctx)
	assert.Equal(t, "/a/b/c/d", resp.Route)
	assert.Empty(t, resp.Data())
}
