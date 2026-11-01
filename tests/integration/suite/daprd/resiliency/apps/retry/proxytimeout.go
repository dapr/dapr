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
	suite.Register(new(proxyTimeout))
}

// proxyTimeout asserts a proxied gRPC call which times out is retried under a
// retry condition matching on status codes: the timeout is the policy's own
// error, not the DeadlineExceeded status the proxied stream fails with.
type proxyTimeout struct {
	caller *daprd.Daprd
	target *daprd.Daprd

	calls atomic.Int32
}

func (p *proxyTimeout) Setup(t *testing.T) []framework.Option {
	const slowCalls = 3

	app := grpcapp.New(t, grpcapp.WithPingFn(func(_ context.Context, in *testpb.PingRequest) (*testpb.PingResponse, error) {
		if p.calls.Add(1) <= slowCalls {
			// Outlive the caller's 500ms timeout on the first attempts.
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
  name: proxytimeout
spec:
  policies:
    timeouts:
      fast: 500ms
    retries:
      threeRetriesOnUnknown:
        policy: constant
        duration: 10ms
        maxRetries: %d
        matching:
          gRPCStatusCodes: "2"
  targets:
    apps:
      %s:
        timeout: fast
        retry: threeRetriesOnUnknown
`, slowCalls, p.target.AppID())),
	)

	return []framework.Option{
		framework.WithProcesses(app, p.target, p.caller),
	}
}

func (p *proxyTimeout) Run(t *testing.T, ctx context.Context) {
	p.target.WaitUntilRunning(t, ctx)
	p.caller.WaitUntilRunning(t, ctx)

	conn, err := grpc.NewClient(p.caller.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	pctx := metadata.AppendToOutgoingContext(ctx, "dapr-app-id", p.target.AppID())
	resp, err := testpb.NewTestServiceClient(conn).Ping(pctx, &testpb.PingRequest{Value: "hello"})
	require.NoError(t, err, "the timed out attempts must be retried until the app answers in time")
	assert.Equal(t, "hello", resp.GetValue())
	// The first call and one retry per timed out attempt.
	assert.Equal(t, int32(4), p.calls.Load())
}
