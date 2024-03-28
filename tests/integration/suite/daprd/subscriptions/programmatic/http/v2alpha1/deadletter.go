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

package v2alpha1

import (
	"context"
	nethttp "net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(deadletter))
}

type deadletter struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (d *deadletter) Setup(t *testing.T) []framework.Option {
	d.sub = subscriber.New(t,
		subscriber.WithRoutes("/b"),
		subscriber.WithHandlerFunc("/a", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			w.WriteHeader(nethttp.StatusServiceUnavailable)
		}),
		subscriber.WithProgrammaticSubscriptions(subscriber.SubscriptionJSON{
			PubsubName: "mypub",
			Topic:      "a",
			Routes: subscriber.RoutesJSON{
				Default: "/a",
			},
			DeadLetterTopic: "mydead",
		},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "mydead",
				Routes: subscriber.RoutesJSON{
					Default: "/b",
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
`))

	return []framework.Option{
		framework.WithProcesses(d.sub, d.daprd),
	}
}

func (d *deadletter) Run(t *testing.T, ctx context.Context) {
	d.daprd.WaitUntilRunning(t, ctx)

	d.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      d.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Data:       `{"status": "completed"}`,
	})

	resp := d.sub.Receive(t, ctx)
	assert.Equal(t, "/b", resp.Route)
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, `{"status": "completed"}`, string(resp.Data()))
}
