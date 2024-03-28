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

package missing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(grpc))
}

type grpc struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (g *grpc) Setup(t *testing.T) []framework.Option {
	g.sub = subscriber.New(t)

	g.daprd = daprd.New(t,
		daprd.WithAppPort(g.sub.Port(t)),
		daprd.WithAppProtocol("grpc"),
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
 name: mysub1
spec:
 pubsubname: anotherpub
 topic: a
 route: /a
---
apiVersion: dapr.io/v1alpha1
kind: Subscription
metadata:
 name: mysub2
spec:
 pubsubname: mypub
 topic: c
 route: /c
`))

	return []framework.Option{
		framework.WithProcesses(g.sub, g.daprd),
	}
}

func (g *grpc) Run(t *testing.T, ctx context.Context) {
	g.daprd.WaitUntilRunning(t, ctx)

	client := g.daprd.GRPCClient(t, ctx)

	meta, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Len(t, meta.GetRegisteredComponents(), 1)
	assert.Len(t, meta.GetSubscriptions(), 2)

	g.sub.ExpectPublishError(t, ctx, g.daprd, &rtv1.PublishEventRequest{
		PubsubName: "anotherpub", Topic: "a", Data: []byte(`{"status": "completed"}`),
	})
	g.sub.ExpectPublishNoReceive(t, ctx, g.daprd, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "b", Data: []byte(`{"status": "completed"}`),
	})
	g.sub.ExpectPublishReceive(t, ctx, g.daprd, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "c", Data: []byte(`{"status": "completed"}`),
	})
}
