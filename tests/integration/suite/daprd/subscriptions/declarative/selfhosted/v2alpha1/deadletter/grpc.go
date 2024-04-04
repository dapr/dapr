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

package deadletter

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd *daprd.Daprd
	app   *app.App
	inCh  chan *rtv1.TopicEventRequest
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.inCh = make(chan *rtv1.TopicEventRequest)
	g.app = app.New(t,
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			if in.GetTopic() == "a" {
				return nil, errors.New("my error")
			}
			g.inCh <- in
			return new(rtv1.TopicEventResponse), nil
		}),
	)

	g.daprd = daprd.New(t,
		daprd.WithAppPort(g.app.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
 deadLetterTopic: mydead
---
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
 name: mysub
spec:
 pubsubname: mypub
 topic: mydead
 routes:
  default: /b
 deadLetterTopic: mydead
`))

	return []framework.Option{
		framework.WithProcesses(g.app, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
		Metadata: map[string]string{"foo": "bar"}, DataContentType: "application/json",
	})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	t.Cleanup(cancel)
	select {
	case <-ctx.Done():
		assert.Fail(t, "timeout waiting for event")
	case in := <-g.inCh:
		assert.Equal(t, "mydead", in.GetTopic())
		assert.Equal(t, "/b", in.GetPath())
	}
}
