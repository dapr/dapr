/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implieh.
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
	suite.Register(new(emptyroute))
}

type emptyroute struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (e *emptyroute) Setup(t *testing.T) []framework.Option {
	e.sub = subscriber.New(t,
		subscriber.WithRoutes("/a"),
	)

	e.daprd = daprd.New(t,
		daprd.WithAppPort(e.sub.Port()),
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
  rules:
  - path: /a
    match: ""
`))

	return []framework.Option{
		framework.WithProcesses(e.sub, e.daprd),
	}
}

func (e *emptyroute) Run(t *testing.T, ctx context.Context) {
	e.daprd.WaitUntilRunning(t, ctx)

	e.sub.Publish(t, ctx, subscriber.PublishRequest{
		Daprd:      e.daprd,
		PubSubName: "mypub",
		Topic:      "a",
		Data:       `{"status": "completed"}`,
	})
	resp := e.sub.Receive(t, ctx)
	assert.Equal(t, "/a", resp.Route)
}
