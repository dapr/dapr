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
	"time"

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
	suite.Register(new(connpool))
}

// connpool verifies pooled-connection semantics against a server that
// advertises an HTTP/2 stream limit. The app enforces
// MaxConcurrentStreams=100 while 150 concurrent InvokeService calls flow
// through daprd. With the pool's shared-ref cap above the server limit,
// excess calls queue at the transport behind the app's advertised limit
// rather than fanning out onto extra connections to circumvent it: all
// calls complete, and the app never observes more in-flight work than it
// asked for. (The pre-raise pool opened a second connection at ref 101,
// silently exceeding the app's declared concurrency.)
type connpool struct {
	daprd       *procdaprd.Daprd
	maxInflight *atomic.Int32
}

const (
	numConcurrentPoolStreams = 150
	appMaxConcurrentStreams  = 100
)

func (c *connpool) Setup(t *testing.T) []framework.Option {
	var inflight atomic.Int32
	var maxInflight atomic.Int32

	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		n := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			seen := maxInflight.Load()
			if n <= seen || maxInflight.CompareAndSwap(seen, n) {
				break
			}
		}

		// Hold the stream briefly so the concurrent callers overlap and
		// the transport-level queueing is actually exercised.
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}

		return new(commonv1.InvokeResponse), nil
	}

	c.maxInflight = &maxInflight

	srv := app.New(t,
		app.WithOnInvokeFn(onInvoke),
		// The app declares its concurrency appetite; the pool must respect
		// it, not defeat it with extra connections.
		app.WithGRPCOptions(procgrpc.WithServerOption(
			func(*testing.T, context.Context) grpc.ServerOption {
				return grpc.MaxConcurrentStreams(appMaxConcurrentStreams)
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

func (c *connpool) Run(t *testing.T, ctx context.Context) {
	c.daprd.WaitUntilRunning(t, ctx)

	conn := c.daprd.GRPCConn(t, ctx)
	client := rtv1.NewDaprClient(conn)

	var wg sync.WaitGroup
	errs := make([]error, numConcurrentPoolStreams)
	for i := range numConcurrentPoolStreams {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
				Id: c.daprd.AppID(),
				Message: &commonv1.InvokeRequest{
					Method:        "pool-test",
					HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_POST},
				},
			})
		}(i)
	}

	wg.Wait()

	for i, err := range errs {
		require.NoErrorf(t, err, "concurrent request %d failed", i)
	}

	maxSeen := c.maxInflight.Load()
	assert.LessOrEqual(t, maxSeen, int32(appMaxConcurrentStreams),
		"the app's advertised stream limit must be respected, not circumvented by extra connections")
	assert.GreaterOrEqual(t, maxSeen, int32(20),
		"requests must genuinely overlap for this test to prove queueing")
}
