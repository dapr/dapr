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

package rawpayload

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapr/components-contrib/pubsub"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/dapr/kit/ptr"
)

func init() {
	suite.Register(new(http))
}

type http struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (h *http) Setup(t *testing.T) []framework.Option {
	h.sub = subscriber.New(t,
		subscriber.WithRoutes("/a"),
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
 metadata:
  rawPayload: "true"
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
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/octet-stream", resp.DataContentType())
	var ce map[string]any
	require.NoError(t, json.NewDecoder(bytes.NewReader(resp.Data())).Decode(&ce))
	exp := pubsub.NewCloudEventsEnvelope("", "", "com.dapr.event.sent", "", "a", "mypub", "text/plain", []byte(`{"status": "completed"}`), "", "")
	exp["id"] = ce["id"]
	exp["source"] = ce["source"]
	exp["time"] = ce["time"]
	exp["traceid"] = ce["traceid"]
	exp["traceparent"] = ce["traceparent"]
	assert.Equal(t, exp, ce)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           h.daprd,
		PubSubName:      "mypub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("application/json"),
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/octet-stream", resp.DataContentType())
	require.NoError(t, json.NewDecoder(bytes.NewReader(resp.Data())).Decode(&ce))
	exp = pubsub.NewCloudEventsEnvelope("", "", "com.dapr.event.sent", "", "a", "mypub", "application/json", []byte(`{"status": "completed"}`), "", "")
	exp["id"] = ce["id"]
	exp["source"] = ce["source"]
	exp["time"] = ce["time"]
	exp["traceid"] = ce["traceid"]
	exp["traceparent"] = ce["traceparent"]
	assert.Equal(t, exp, ce)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:           h.daprd,
		PubSubName:      "mypub",
		Topic:           "a",
		Data:            `{"status": "completed"}`,
		DataContentType: ptr.Of("foo/bar"),
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/octet-stream", resp.DataContentType())
	require.NoError(t, json.NewDecoder(bytes.NewReader(resp.Data())).Decode(&ce))
	exp = pubsub.NewCloudEventsEnvelope("", "", "com.dapr.event.sent", "", "a", "mypub", "application/json", nil, "", "")
	exp["id"] = ce["id"]
	exp["data"] = `{"status": "completed"}`
	exp["source"] = ce["source"]
	exp["time"] = ce["time"]
	exp["traceid"] = ce["traceid"]
	exp["traceparent"] = ce["traceparent"]
	exp["datacontenttype"] = "foo/bar"
	assert.Equal(t, exp, ce)

	h.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      h.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Data:       "foo",
	})
	resp = h.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
	assert.Equal(t, "1.0", resp.SpecVersion())
	assert.Equal(t, "mypub", resp.Extensions()["pubsubname"])
	assert.Equal(t, "a", resp.Extensions()["topic"])
	assert.Equal(t, "com.dapr.event.sent", resp.Type())
	assert.Equal(t, "application/octet-stream", resp.DataContentType())
	require.NoError(t, json.NewDecoder(bytes.NewReader(resp.Data())).Decode(&ce))
	exp = pubsub.NewCloudEventsEnvelope("", "", "com.dapr.event.sent", "", "a", "mypub", "application/json", []byte("foo"), "", "")
	exp["id"] = ce["id"]
	exp["source"] = ce["source"]
	exp["time"] = ce["time"]
	exp["traceid"] = ce["traceid"]
	exp["traceparent"] = ce["traceparent"]
	exp["datacontenttype"] = "text/plain"
	assert.Equal(t, exp, ce)
}
