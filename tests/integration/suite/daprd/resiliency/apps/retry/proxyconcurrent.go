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

package retry

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	grpcapp "github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(proxyConcurrent))
}

// proxyConcurrent proxies many concurrent gRPC calls whose first attempt
// outlives the resiliency timeout, so every call has a timed-out attempt still
// running while its retry starts. Each attempt must keep its own state: every
// call must succeed and the caller daprd must stay up.
type proxyConcurrent struct {
	caller *daprd.Daprd
	target *daprd.Daprd

	seen     sync.Map
	attempts atomic.Int64
}

func (p *proxyConcurrent) Setup(t *testing.T) []framework.Option {
	app := grpcapp.New(t, grpcapp.WithPingFn(func(_ context.Context, in *testpb.PingRequest) (*testpb.PingResponse, error) {
		p.attempts.Add(1)
		if _, retried := p.seen.LoadOrStore(in.GetValue(), struct{}{}); !retried {
			// Outlive the caller's 500ms timeout on each call's first attempt.
			time.Sleep(time.Second * 2)
		}
		return &testpb.PingResponse{Value: in.GetValue()}, nil
	}))

	p.target = daprd.New(t,
		daprd.WithAppPort(app.Port(t)),
		daprd.WithAppProtocol("grpc"),
	)

	p.caller = daprd.New(t,
		daprd.WithResourceFiles(fmt.Sprintf(`
apiVersion: dapr.io/v1alpha1
kind: Resiliency
metadata:
  name: proxyconcurrent
spec:
  policies:
    timeouts:
      fast: 500ms
    retries:
      threeRetries:
        policy: constant
        duration: 10ms
        maxRetries: 3
  targets:
    apps:
      %s:
        timeout: fast
        retry: threeRetries
`, p.target.AppID())),
	)

	return []framework.Option{
		framework.WithProcesses(app, p.target, p.caller),
	}
}

func (p *proxyConcurrent) Run(t *testing.T, ctx context.Context) {
	p.target.WaitUntilRunning(t, ctx)
	p.caller.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(p.caller.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	client := testpb.NewTestServiceClient(conn)
	pctx := metadata.AppendToOutgoingContext(ctx, "dapr-app-id", p.target.AppID())

	const callers, calls = 20, 10

	call := func(value string) error {
		resp, err := client.Ping(pctx, &testpb.PingRequest{Value: value})
		if err != nil {
			return fmt.Errorf("call %s: %w", value, err)
		}
		if resp.GetValue() != value {
			return fmt.Errorf("call %s answered %s", value, resp.GetValue())
		}
		return nil
	}

	var wg sync.WaitGroup
	for c := range callers {
		wg.Go(func() {
			for i := range calls {
				if err := call(fmt.Sprintf("%d-%d", c, i)); err != nil {
					assert.NoError(t, err, "a timed out attempt must be retried until the app answers in time")
					return
				}
			}
		})
	}
	wg.Wait()

	assert.GreaterOrEqual(t, p.attempts.Load(), int64(2*callers*calls),
		"every call's first attempt must time out and be retried")
	p.caller.WaitUntilRunning(t, ctx)
}
