/*
Copyright 2026 The Dapr Authors
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

package subscriptions

/*
import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appredis))
}

type appredis struct {
	daprd *daprd.Daprd
	app   *app.App

	appPort int

	retryEnabled atomic.Bool
	eventCh      chan *rtv1.TopicEventRequest
}

func (a *appredis) Setup(t *testing.T) []framework.Option {
	a.eventCh = make(chan *rtv1.TopicEventRequest, 10)
	a.retryEnabled.Store(true)
	a.appPort = appredisFreePort(t)
	a.app = a.newApp(t)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(a.appPort),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithResourceFiles(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: dapr-resiliency
spec:
  policies:
    retries:
      retryAppCall:
        duration: 1s
        maxRetries: 4
        policy: constant
  targets:
    components:
      mypub:
        inbound:
          retry: retryAppCall
`,
			`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypub
spec:
  type: pubsub.redis
  version: v1
  metadata:
  - name: redisHost
    value: localhost:6379
  - name: redisPassword
    value: ""
  - name: processingTimeout
    value: "0"
  - name: redeliverInterval
    value: "0"
`,
		),
	)

	return []framework.Option{
		framework.WithProcesses(a.app, a.daprd),
	}
}

func (a *appredis) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	_, err := a.daprd.GRPCClient(t, ctx).PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypub", Topic: "a",
		Data:            []byte(`{"status": "completed"}`),
		DataContentType: "application/json",
	})
	require.NoError(t, err)

	firstEvent := a.receiveEvent(t, ctx, time.Second*5)
	require.NotNil(t, firstEvent)

	a.app.Cleanup(t)

	t.Log("waiting for app to restart...")
	time.Sleep(time.Second * 2)

	a.retryEnabled.Store(false)
	a.app = a.newApp(t)
	a.app.Run(t, ctx)

	t.Log("receiving redelivery event...")
	redelivery := a.receiveEvent(t, ctx, time.Second*5)
	t.Log("received redelivery event", redelivery)
	assert.NotNil(t, redelivery, "expected redelivery after app restart")
}

func (a *appredis) newApp(t *testing.T) *app.App {
	t.Helper()

	return app.New(t,
		app.WithGRPCOptions(procgrpc.WithListener(func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(a.appPort))
		})),
		app.WithOnTopicEventFn(func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			select {
			case a.eventCh <- in:
			case <-ctx.Done():
			}
			if a.retryEnabled.Load() {
				return &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_RETRY}, nil
			}
			return &rtv1.TopicEventResponse{Status: rtv1.TopicEventResponse_SUCCESS}, nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
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
}

func (a *appredis) receiveEvent(t *testing.T, ctx context.Context, timeout time.Duration) *rtv1.TopicEventRequest {
	t.Helper()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		require.Fail(t, "timed out waiting for app delivery")
		return nil
	case event := <-a.eventCh:
		t.Log("received event", event)
		return event
	}
}

func appredisFreePort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())
	return port
}
*/
