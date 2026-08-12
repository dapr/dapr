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
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(connpoolgrow))
}

type connpoolgrow struct {
	daprd      *procdaprd.Daprd
	allArrived chan struct{}
}

const (
	// One past the pool's shared-ref cap (grpcMaxConcurrentStreams=2048) plus
	// the second connection's stream window.
	numGrowCalls        = 2149
	growAppStreamLimit  = 100
	growBarrierInflight = 2 * growAppStreamLimit
)

func (c *connpoolgrow) Setup(t *testing.T) []framework.Option {
	c.allArrived = make(chan struct{})
	var once sync.Once
	var inflight atomic.Int32

	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		n := inflight.Add(1)
		defer inflight.Add(-1)

		if int(n) >= growBarrierInflight {
			once.Do(func() { close(c.allArrived) })
		}

		select {
		case <-c.allArrived:
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		return new(commonv1.InvokeResponse), nil
	}

	srv := app.New(t,
		app.WithOnInvokeFn(onInvoke),
		app.WithGRPCOptions(procgrpc.WithServerOption(
			func(*testing.T, context.Context) grpc.ServerOption {
				return grpc.MaxConcurrentStreams(growAppStreamLimit)
			},
		)),
	)
	c.daprd = procdaprd.New(t,
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(srv.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, c.daprd),
	}
}

func (c *connpoolgrow) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	conn := c.daprd.GRPCConn(t, ctx)
	client := rtv1.NewDaprClient(conn)

	var wg sync.WaitGroup
	errs := make([]error, numGrowCalls)
	for i := range numGrowCalls {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
				Id: c.daprd.AppID(),
				Message: &commonv1.InvokeRequest{
					Method:        "pool-grow-test",
					HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_POST},
				},
			})
		}(i)
	}

	wg.Wait()

	select {
	case <-c.allArrived:
	default:
		require.Fail(t, "the pool never grew a second connection: 200 simultaneous streams were not observed")
	}
	for i, err := range errs {
		assert.NoErrorf(t, err, "concurrent request %d failed", i)
	}
}
