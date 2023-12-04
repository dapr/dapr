/*
Copyright 2023 The Dapr Authors
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

package grpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appready))
}

type appready struct {
	daprd        *daprd.Daprd
	appHealthy   atomic.Bool
	healthCalled atomic.Int64
	topicChan    chan string
}

func (a *appready) Setup(t *testing.T) []framework.Option {
	srv := app.New(t,
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			defer a.healthCalled.Add(1)
			if a.appHealthy.Load() {
				return &rtv1.HealthCheckResponse{}, nil
			}
			return nil, errors.New("app not healthy")
		}),
		app.WithOnTopicEventFn(func(_ context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
			a.topicChan <- in.GetPath()
			return new(rtv1.TopicEventResponse), nil
		}),
		app.WithListTopicSubscriptions(func(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
			return &rtv1.ListTopicSubscriptionsResponse{
				Subscriptions: []*rtv1.TopicSubscription{
					{PubsubName: "mypubsub", Topic: "mytopic", Routes: &rtv1.TopicRoutes{Default: "/myroute"}},
				},
			}, nil
		}),
	)

	a.topicChan = make(chan string)
	a.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mypubsub
spec:
  type: pubsub.in-memory
  version: v1`),
	)

	return []framework.Option{
		framework.WithProcesses(srv, a.daprd),
	}
}

func (a *appready) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)
	httpClient := util.HTTPClient(t)

	reqURL := fmt.Sprintf("http://localhost:%d/v1.0/invoke/%s/method/foo", a.daprd.HTTPPort(), a.daprd.AppID())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	require.NoError(t, err)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		var resp *rtv1.GetMetadataResponse
		resp, err = client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, time.Millisecond*100)

	called := a.healthCalled.Load()
	require.Eventually(t, func() bool { return a.healthCalled.Load() > called }, time.Second*5, time.Millisecond*100)

	assert.Eventually(t, func() bool {
		var resp *http.Response
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*5, 100*time.Millisecond)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypubsub",
		Topic:      "mytopic",
		Data:       []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)

	called = a.healthCalled.Load()
	require.Eventually(t, func() bool {
		select {
		case <-a.topicChan:
			assert.Fail(t, "unexpected publish")
		default:
		}
		return a.healthCalled.Load() > called
	}, time.Second*5, time.Millisecond*100)

	a.appHealthy.Store(true)

	assert.Eventually(t, func() bool {
		var resp *http.Response
		resp, err = util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Second*5, 100*time.Millisecond)

	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypubsub",
		Topic:      "mytopic",
		Data:       []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)

	select {
	case resp := <-a.topicChan:
		assert.Equal(t, "/myroute", resp)
	case <-time.After(time.Second * 5):
		assert.Fail(t, "timeout waiting for topic to return")
	}

	// Should stop sending messages to subscribed app when it becomes unhealthy.
	a.appHealthy.Store(false)
	assert.Eventually(t, func() bool {
		var resp *http.Response
		resp, err = httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*5, 100*time.Millisecond)
	_, err = client.PublishEvent(ctx, &rtv1.PublishEventRequest{
		PubsubName: "mypubsub",
		Topic:      "mytopic",
		Data:       []byte(`{"status": "completed"}`),
	})
	require.NoError(t, err)

	called = a.healthCalled.Load()
	require.Eventually(t, func() bool {
		select {
		case <-a.topicChan:
			assert.Fail(t, "unexpected publish")
		default:
		}
		return a.healthCalled.Load() > called
	}, time.Second*5, time.Millisecond*100)
}
