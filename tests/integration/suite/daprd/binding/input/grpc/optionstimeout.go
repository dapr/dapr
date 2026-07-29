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

package grpc

import (
	"context"
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
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(optionstimeout))
}

// optionstimeout checks --app-binding-options-timeout bounds the gRPC discovery
// probe, which uses ListInputBindings instead of HTTP OPTIONS.
//
// Each daprd gets its own app because OnBindingEvent carries no daprd identity.
type optionstimeout struct {
	failApp   *app.App
	customApp *app.App

	daprdFail   *daprd.Daprd
	daprdCustom *daprd.Daprd

	bindingCalledFail   atomic.Int64
	bindingCalledCustom atomic.Int64
}

func (o *optionstimeout) Setup(t *testing.T) []framework.Option {
	const (
		// Stands in for a slow-starting app, ex: a JVM/JIT workload.
		optionsDelay = 2 * time.Second

		failTimeout   = optionsDelay / 2 // probe gives up first
		customTimeout = 5 * optionsDelay // app answers first
	)

	// direction is omitted so daprd runs the probe instead of skipping it.
	const bindingResource = `
apiVersion: dapr.io/v1alpha1
kind: Component
metadata:
  name: mybinding
spec:
  type: bindings.cron
  version: v1
  metadata:
  - name: schedule
    value: "@every 1s"
`

	listInputBindings := func(delay time.Duration) func(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
		return func(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
			time.Sleep(delay)
			return &rtv1.ListInputBindingsResponse{Bindings: []string{"mybinding"}}, nil
		}
	}

	o.failApp = app.New(t,
		app.WithListInputBindings(listInputBindings(optionsDelay)),
		app.WithOnBindingEventFn(func(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			o.bindingCalledFail.Add(1)
			return new(rtv1.BindingEventResponse), nil
		}),
	)

	o.customApp = app.New(t,
		app.WithListInputBindings(listInputBindings(optionsDelay)),
		app.WithOnBindingEventFn(func(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
			o.bindingCalledCustom.Add(1)
			return new(rtv1.BindingEventResponse), nil
		}),
	)

	// A failed gRPC probe reads as an empty subscription list, so daprd keeps
	// running with the binding inactive.
	o.daprdFail = daprd.New(t,
		daprd.WithAppPort(o.failApp.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppBindingOptionsTimeout(failTimeout),
		daprd.WithResourceFiles(bindingResource),
	)

	o.daprdCustom = daprd.New(t,
		daprd.WithAppPort(o.customApp.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppBindingOptionsTimeout(customTimeout),
		daprd.WithResourceFiles(bindingResource),
	)

	return []framework.Option{
		framework.WithProcesses(o.failApp, o.customApp, o.daprdFail, o.daprdCustom),
	}
}

func (o *optionstimeout) Run(t *testing.T, ctx context.Context) {
	// The probe runs during startup, so by the time daprd is up it has already
	// timed out or succeeded.
	o.daprdFail.WaitUntilRunning(t, ctx)
	o.daprdCustom.WaitUntilRunning(t, ctx)

	gclientFail := o.daprdFail.GRPCClient(t, ctx)
	gclientCustom := o.daprdCustom.GRPCClient(t, ctx)

	// Registered either way, a failed probe only stops it reading.
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := gclientFail.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		assert.NoError(c, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, 10*time.Millisecond)

	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err := gclientCustom.GetMetadata(ctx, new(rtv1.GetMetadataRequest))
		assert.NoError(c, err)
		assert.Len(c, resp.GetRegisteredComponents(), 1)
	}, time.Second*5, 10*time.Millisecond)

	require.Eventually(t, func() bool {
		return o.bindingCalledCustom.Load() > 0
	}, 5*time.Second, 100*time.Millisecond, "binding should fire: probe had time to succeed")

	assert.Equal(t, int64(0), o.bindingCalledFail.Load(),
		"binding should not fire: probe timed out")
}
