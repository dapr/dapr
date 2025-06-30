/*
Copyright 2025 The Dapr Authors
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

package pubsub

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	"github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(inflight))
}

type inflight struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
	healthz     atomic.Bool
}

func (i *inflight) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	i.closeInvoke = make(chan struct{})
	i.healthz.Store(true)

	app := app.New(t,
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			if i.healthz.Load() {
				return new(rtv1.HealthCheckResponse), nil
			}
			return nil, errors.New("not healthy")
		}),
		app.WithOnTopicEventFn(func(context.Context, *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			i.inInvoke.Store(true)
			<-i.closeInvoke
			return nil, nil
		}),
	)

	i.broker = broker.New(t)

	i.daprd = daprd.New(t,
		i.broker.DaprdOptions(t, "mypub",
			daprd.WithDaprBlockShutdownDuration("180s"),
			daprd.WithAppHealthProbeInterval(1),
			daprd.WithAppHealthProbeThreshold(1),
			daprd.WithAppHealthCheck(true),
			daprd.WithAppPort(app.Port(t)),
			daprd.WithAppProtocol("grpc"),
			daprd.WithResourceFiles(`
apiVersion: dapr.io/v2alpha1
kind: Subscription
metadata:
  name: mysub
spec:
  pubsubname: mypub
  topic: a
  routes:
    default: /a
`),
		)...,
	)

	return []framework.Option{
		framework.WithProcesses(app, i.broker),
	}
}

func (i *inflight) Run(t *testing.T, ctx context.Context) {
	i.daprd.Run(t, ctx)
	i.daprd.WaitUntilRunning(t, ctx)
	t.Cleanup(func() { i.daprd.Cleanup(t) })

	assert.Len(t, i.daprd.GetMetaSubscriptions(t, ctx), 1)

	ch := i.broker.PublishHelloWorld("a")

	require.Eventually(t, i.inInvoke.Load, time.Second*10, time.Millisecond*10)

	go i.daprd.Cleanup(t)

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	close(i.closeInvoke)

	select {
	case req := <-ch:
		assert.Nil(t, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = i.broker.PublishHelloWorld("a")
	select {
	case req := <-ch:
		assert.Equal(t, &components.AckMessageError{Message: "subscription is closed"}, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	client := i.daprd.GRPCClient(t, ctx)
	req := &rtv1.InvokeServiceRequest{
		Id: i.daprd.AppID(),
		Message: &commonv1.InvokeRequest{
			Method:        "foo",
			HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_POST},
		},
	}
	_, err := client.InvokeService(ctx, req)
	require.NoError(t, err)

	i.healthz.Store(false)

	assert.EventuallyWithT(t, func(c *assert.CollectT) {
		_, err = client.InvokeService(ctx, req)
		assert.NoError(c, err)
	}, time.Second*10, time.Millisecond*10)
}
