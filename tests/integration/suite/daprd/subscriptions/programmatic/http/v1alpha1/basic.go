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
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(basic))
}

type basic struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (b *basic) Setup(t *testing.T) []framework.Option {
	b.sub = subscriber.New(t,
		subscriber.WithRoutes("/a"),
		subscriber.WithProgrammaticSubscriptions(subscriber.SubscriptionJSON{
			PubsubName: "mypub",
			Topic:      "a",
			Route:      "/a",
		}),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.sub.Port()),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(b.sub, b.daprd),
	}
}

func (b *basic) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)

	b.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      b.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Data:       `{"status": "completed"}`,
	})
	resp := b.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "text/plain", resp.DataContentType())

	b.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           b.daprd,
		PubSubName:      "mypub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("application/json"),
	})
	resp = b.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/json", resp.DataContentType())

	b.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           b.daprd,
		PubSubName:      "mypub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("foo/bar"),
	})
	resp = b.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "foo/bar", resp.DataContentType())

	b.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      b.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Data:       "foo",
	})
	resp = b.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Equal(t, "foo", string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "text/plain", resp.DataContentType())
}
