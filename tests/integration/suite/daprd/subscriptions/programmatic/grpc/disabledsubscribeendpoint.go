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

package grpc

import (
	"context"
	"testing"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/subscriber"
	"github.com/dapr/dapr/tests/integration/framework/process/logline"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
)

func init() {
	suite.Register(new(disablesubscribeendpoint))
}

type disablesubscribeendpoint struct {
	daprd             *daprd.Daprd
	logLineAppWaiting *logline.LogLine
	sub               *subscriber.Subscriber
}

func (b *disablesubscribeendpoint) Setup(t *testing.T) []framework.Option {
	b.logLineAppWaiting = logline.New(t, logline.WithStdoutLineContains(
		"Skipping programmatic subscription loading",
	))

	b.sub = subscriber.New(t,
		subscriber.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{
						PubsubName: "mypub",
						Topic:      "a",
						Routes: &rtv1.TopicRoutes{
							Default: "/a",
						},
					},
				},
			}, nil
		}),
	)

	b.daprd = daprd.New(t,
		daprd.WithAppPort(b.sub.Port(t)),
		daprd.WithDisableInitEndpoints("subscribe"),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.in-memory
  version: v1
`),
		daprd.WithExecOptions(exec.WithStdout(b.logLineAppWaiting.Stdout())),
	)

	return []framework.Option{
		framework.WithProcesses(b.sub, b.logLineAppWaiting, b.daprd),
	}
}

func (b *disablesubscribeendpoint) Run(t *testing.T, ctx context.Context) {
	b.daprd.WaitUntilRunning(t, ctx)
	b.logLineAppWaiting.EventuallyFoundAll(t)

	client := b.daprd.GRPCClient(t, ctx)

	_, err := client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a", Data: []byte(`{"status": "completed"}`),
		Metadata: map[string]string{"foo": "bar"}, DataContentType: "application/json",
	})
	require.NoError(t, err)
	b.sub.AssertEventChanLen(t, 0)
}
