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

package invocation

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/os"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(callee))
}

type callee struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd

	inInvoke    atomic.Bool
	healthError atomic.Bool
	closeInvoke chan struct{}
}

func (c *callee) Setup(t *testing.T) []framework.Option {
	os.SkipWindows(t)

	c.closeInvoke = make(chan struct{})

	app := app.New(t,
		app.WithHealthCheckFn(func(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
			if c.healthError.Load() {
				return nil, assert.AnError
			}
			return nil, nil
		}),
		app.WithOnInvokeFn(func(context.Context, *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
			c.inInvoke.Store(true)
			<-c.closeInvoke
			return nil, nil
		}),
	)

	opts := []daprd.Option{
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithDaprBlockShutdownDuration("180s"),
		daprd.WithAppHealthProbeInterval(1),
		daprd.WithAppHealthProbeThreshold(1),
		daprd.WithAppHealthCheck(true),
	}

	c.daprd1 = daprd.New(t, opts...)
	c.daprd2 = daprd.New(t, opts...)

	return []framework.Option{
		framework.WithProcesses(app, c.daprd1),
	}
}

func (c *callee) Run(t *testing.T, ctx context.Context) {
	c.daprd1.WaitUntilRunning(t, ctx)
	c.daprd2.Run(t, ctx)
	c.daprd2.WaitUntilRunning(t, ctx)
	t.Cleanup(func() {
		c.healthError.Store(true)
		c.daprd2.Cleanup(t)
	})

	client := c.daprd1.GRPCClient(t, ctx)

	errCh := make(chan error)
	go func() {
		_, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: c.daprd2.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "foo",
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_POST},
			},
		})

		errCh <- err
	}()

	require.Eventually(t, c.inInvoke.Load, time.Second*10, time.Millisecond*10)

	go c.daprd2.Cleanup(t)

	select {
	case err := <-errCh:
		assert.Fail(t, "unexpected error returned", err)
	case <-time.After(time.Second * 3):
	}

	close(c.closeInvoke)

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(time.Second * 10):
		assert.Fail(t, "timeout")
	}
}
