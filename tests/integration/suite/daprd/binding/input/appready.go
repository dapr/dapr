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

package input

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

	bindingCalled atomic.Int64
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
		app.WithOnBindingEventFn(func(ctx context.Context, in *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			switch in.GetName() {
			case "mybinding":
				a.bindingCalled.Add(1)
			default:
				assert.Failf(t, "unexpected binding name", "binding name: %s", in.GetName())
			}
			return new(rtv1.BindingEventResponse), nil
		}),
		app.WithListInputBindings(func(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
			return &rtv1.ListInputBindingsResponse{
				Bindings: []string{"mybinding"},
			}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppHealthCheck(true),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithResourceFiles(`apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: 'mybinding'
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
  - name: direction
    value: "input"
`))

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
		resp, err := client.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		require.NoError(t, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, time.Millisecond*100)

	called := a.healthCalled.Load()
	require.Eventually(t, func() bool { return a.healthCalled.Load() > called }, time.Second*5, time.Millisecond*100)

	assert.Eventually(t, func() bool {
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*5, 100*time.Millisecond)

	time.Sleep(time.Second * 2)
	assert.Equal(t, int64(0), a.bindingCalled.Load())

	a.appHealthy.Store(true)

	assert.Eventually(t, func() bool {
		resp, err := util.HTTPClient(t).Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, time.Second*5, 100*time.Millisecond)

	assert.Eventually(t, func() bool {
		return a.bindingCalled.Load() > 0
	}, time.Second*5, 100*time.Millisecond)

	// Should stop calling binding when app becomes unhealthy
	a.appHealthy.Store(false)
	assert.Eventually(t, func() bool {
		resp, err := httpClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusInternalServerError
	}, time.Second*5, 100*time.Millisecond)
	called = a.bindingCalled.Load()
	time.Sleep(time.Second * 2)
	assert.Equal(t, called, a.bindingCalled.Load())
}
