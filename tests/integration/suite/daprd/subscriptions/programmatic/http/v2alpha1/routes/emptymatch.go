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

package routes

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
	suite.Register(new(emptymatch))
}

type emptymatch struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (e *emptymatch) Setup(t *testing.T) []framework.Option {
	e.sub = subscriber.New(t,
		subscriber.WithRoutes(
			"/justpath", "/abc", "/123", "/def", "/zyz",
			"/aaa", "/bbb", "/xyz", "/456", "/789",
		),
		subscriber.WithProgrammaticSubscriptions(
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "justpath",
				Routes: subscriber.RoutesJSON{
					Rules: []*subscriber.RuleJSON{
						{Path: "/justpath", Match: ""},
					},
				},
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "defaultandpath",
				Routes: subscriber.RoutesJSON{
					Default: "/abc",
					Rules: []*subscriber.RuleJSON{
						{Path: "/123", Match: ""},
					},
				},
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "multipaths",
				Routes: subscriber.RoutesJSON{
					Rules: []*subscriber.RuleJSON{
						{Path: "/xyz", Match: ""},
						{Path: "/456", Match: ""},
						{Path: "/789", Match: ""},
					},
				},
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "defaultandpaths",
				Routes: subscriber.RoutesJSON{
					Default: "/def",
					Rules: []*subscriber.RuleJSON{
						{Path: "/zyz", Match: ""},
						{Path: "/aaa", Match: ""},
						{Path: "/bbb", Match: ""},
					},
				},
			},
		),
	)

	e.daprd = daprd.New(t,
		daprd.WithAppPort(e.sub.Port()),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(e.sub, e.daprd),
	}
}

func (e *emptymatch) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	e.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      e.daprd,
		PubSubName: "mypub",
		Topic:      "justpath",
	})
	resp := e.sub.Receive(t, ctx)
	assert.Equal(t, "/justpath", resp.Route)

	e.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      e.daprd,
		PubSubName: "mypub",
		Topic:      "defaultandpath",
	})
	resp = e.sub.Receive(t, ctx)
	assert.Equal(t, "/123", resp.Route)

	e.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      e.daprd,
		PubSubName: "mypub",
		Topic:      "multipaths",
	})
	resp = e.sub.Receive(t, ctx)
	assert.Equal(t, "/xyz", resp.Route)

	e.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      e.daprd,
		PubSubName: "mypub",
		Topic:      "defaultandpaths",
	})
	resp = e.sub.Receive(t, ctx)
	assert.Equal(t, "/zyz", resp.Route)
}
