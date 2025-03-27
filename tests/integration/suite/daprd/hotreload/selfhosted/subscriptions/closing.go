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

package subscriptions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/dapr/dapr/pkg/proto/components/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/pubsub/broker"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(closing))
}

type closing struct {
	daprd  *daprd.Daprd
	broker *broker.Broker

	inInvoke    atomic.Bool
	closeInvoke chan struct{}
	dir         string
}

func (c *closing) Setup(t *testing.T) []framework.Option {
	c.dir = t.TempDir()

	c.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{PubsubName: "mypub", Topic: "a", Routes: &rtv1.TopicRoutes{Default: "/a"}},
				},
			}, nil
		}),
		app.WithOnTopicEventFn(func(context.Context, *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			c.inInvoke.Store(true)
			<-c.closeInvoke
			return nil, nil
		}),
	)

	c.broker = broker.New(t)

	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "comp.yaml"), []byte(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
 name: 'mypub'
spec:
 type: pubsub.%s
 version: v1
`, c.broker.PubSub().SocketName())), 0o600))

	c.daprd = daprd.New(t,
		daprd.WithSocket(t, c.broker.Socket()),
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithResourcesDir(c.dir),
		daprd.WithConfigManifests(t, `
apiVersion: dapr.io/v1alpha1
kind: Configun
metadata:
  name: hotreloading
spec:
  features:
  - name: HotReload
    enabled: true`),
	)

	return []framework.Option{
		framework.WithProcesses(app, c.broker, c.daprd),
	}
}

func (c *closing) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)
	assert.Len(t, c.daprd.GetMetaSubscriptions(t, ctx), 1)

	ch := c.broker.PublishHelloWorld("a")

	require.Eventually(t, c.inInvoke.Load, time.Second*10, time.Millisecond*10)

	require.NoError(t, os.WriteFile(filepath.Join(c.dir, "comp.yaml"), nil, 0o600))

	select {
	case req := <-ch:
		assert.Fail(t, "unexpected request returned", req)
	case <-time.After(time.Second * 3):
	}

	close(c.closeInvoke)

	select {
	case req := <-ch:
		assert.Nil(t, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	ch = c.broker.PublishHelloWorld("a")
	select {
	case req := <-ch:
		assert.Equal(t, &components.AckMessageError{Message: "subscription is closed"}, req.GetAckError())
		assert.Equal(t, "foo", req.GetAckMessageId())
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}

	// The Subscription should eventually be completely removed.
	assert.EventuallyWithT(t, func(col *assert.CollectT) {
		ch = c.broker.PublishHelloWorld("a")
		select {
		case req := <-ch:
			assert.Fail(col, "unexpected request returned", req)
		case <-time.After(time.Second):
		}
	}, time.Second*10, time.Millisecond*10)
}
