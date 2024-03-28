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
	"github.com/stretchr/testify/require"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/http/subscriber"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(missing))
}

type missing struct {
	daprd *daprd.Daprd
	sub   *subscriber.Subscriber
}

func (m *missing) Setup(t *testing.T) []framework.Option {
	m.sub = subscriber.New(t,
		subscriber.WithRoutes("/a", "/b", "/c"),
		subscriber.WithProgrammaticSubscriptions(
			subscriber.SubscriptionJSON{
				PubsubName: "anotherpub",
				Topic:      "a",
				Route:      "/a",
			},
			subscriber.SubscriptionJSON{
				PubsubName: "mypub",
				Topic:      "c",
				Route:      "/c",
			},
		),
	)

	m.daprd = daprd.New(t,
		daprd.WithAppPort(m.sub.Port()),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`))

	return []framework.Option{
		framework.WithProcesses(m.sub, m.daprd),
	}
}

func (m *missing) Run(t *testing.T, ctx context.Context) {
	m.daprd.WaitUntilRunning(t, ctx)

	meta, err := m.daprd.GRPCClient(t, ctx).GetMetadata(ctx, new(rtv1.GetMetadataRequest))
	require.NoError(t, err)
	assert.Len(t, meta.GetRegisteredComponents(), 1)
	assert.Len(t, meta.GetSubscriptions(), 2)

	m.sub.ExpectPublishError(t, ctx, subscriber.PublishRequest{
		Daprd:      m.daprd,
		PubSubName: "anotherpub",
		Topic:      "a",
		Data:       `{"status": "completed"}`,
	})

	m.sub.ExpectPublishNoReceive(t, ctx, subscriber.PublishRequest{
		Daprd:      m.daprd,
		PubSubName: "mypub",
		Topic:      "b",
		Data:       `{"status": "completed"}`,
	})

	m.sub.ExpectPublishReceive(t, ctx, subscriber.PublishRequest{
		Daprd:      m.daprd,
		PubSubName: "mypub",
		Topic:      "c",
		Data:       `{"status": "completed"}`,
	})
}
