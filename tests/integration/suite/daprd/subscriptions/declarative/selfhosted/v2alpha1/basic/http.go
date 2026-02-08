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

package basic

import (
	"context"
	nethttp "net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/concurrency/slice"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd              *daprd.Daprd
	sub                *subscriber.Subscriber
	notfoundReceived   slice.Slice[string]
	deadLetterReceived slice.Slice[string]
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.notfoundReceived = slice.String()
	h.deadLetterReceived = slice.String()

	h.sub = subscriber.New(t,
		subscriber.WithRoutes("/a"),
		subscriber.WithHandlerFunc("/notfound", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			h.notfoundReceived.Append(r.URL.Path)
			w.WriteHeader(nethttp.StatusNotFound)
		}),
		subscriber.WithHandlerFunc("/deadletter", func(w nethttp.ResponseWriter, r *nethttp.Request) {
			h.deadLetterReceived.Append(r.URL.Path)
		}),
	)

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
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: mysub
spec:
  pubsubname: mypub
  topic: a
  routes:
    default: /a
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: notfoundsub
spec:
  pubsubname: mypub
  topic: notfound
  routes:
    default: /notfound
  deadLetterTopic: deadletter
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: deadlettersub
spec:
  pubsubname: mypub
  topic: deadletter
  routes:
    default: /deadletter
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
		Data:       `{"status": "completed"}`,
	})
	resp := h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "text/plain", resp.DataContentType())

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           h.daprd,
		PubSubName:      "mypub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("application/json"),
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/json", resp.DataContentType())

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           h.daprd,
		PubSubName:      "mypub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("foo/bar"),
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.JSONEq(t, `{"status": "completed"}`, string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "foo/bar", resp.DataContentType())

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Data:       "foo",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Equal(t, "foo", string(resp.Data()))
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "text/plain", resp.DataContentType())

	// Test that 404 response from subscriber drops the message and sends it to the dead letter topic
	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "notfound",
		Data:       `{"status": "test"}`,
	})

	// Wait for the message to be delivered to the handler
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"/notfound"}, h.notfoundReceived.Slice())
	}, time.Second*10, time.Millisecond*10)

	// Wait for the message to be delivered to the dead letter topic
	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		assert.ElementsMatch(c, []string{"/deadletter"}, h.deadLetterReceived.Slice())
	}, time.Second*10, time.Millisecond*10)
}
