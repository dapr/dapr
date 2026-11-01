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

package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(traceparentproxy))
}

// traceparentproxy is a regression test for #10563: on a proxied gRPC service
// invocation, when the caller provides a traceparent, the callee app must receive
// exactly one traceparent (the current span) rather than the forwarded caller
// value plus Dapr's own appended one (which downstream W3C propagators comma-join
// and reject).
type traceparentproxy struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	ch     chan metadata.MD
}

func (p *traceparentproxy) Setup(t *testing.T) []framework.Option {
	p.ch = make(chan metadata.MD, 1)

	srv := app.New(t,
		app.WithPingFn(func(ctx context.Context, _ *testpb.PingRequest) (*testpb.PingResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			p.ch <- md
			return new(testpb.PingResponse), nil
		}),
	)

	tracingConfig := `apiVersion: dapr.io/v1alpha1
kind: Configuration
metadata:
  name: tracing
spec:
  tracing:
    samplingRate: "1.0"
`

	p.daprd1 = daprd.New(t,
		daprd.WithAppID("app1"),
		daprd.WithAppProtocol("grpc"),
		daprd.WithConfigManifests(t, tracingConfig),
	)
	p.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithConfigManifests(t, tracingConfig),
	)

	return []framework.Option{
		framework.WithProcesses(srv, p.daprd1, p.daprd2),
	}
}

func (p *traceparentproxy) Run(t *testing.T, ctx context.Context) {
	p.daprd1.WaitUntilRunning(t, ctx)
	p.daprd2.WaitUntilRunning(t, ctx)

	client := testpb.NewTestServiceClient(p.daprd1.GRPCConn(t, ctx))
	ctx = metadata.AppendToOutgoingContext(ctx,
		"dapr-app-id", p.daprd2.AppID(),
		"traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
	)
	_, err := client.Ping(ctx, new(testpb.PingRequest))
	require.NoError(t, err)

	select {
	case md := <-p.ch:
		t.Logf("callee received %d traceparent header(s): %v", len(md.Get("traceparent")), md.Get("traceparent"))
		assert.Len(t, md.Get("traceparent"), 1,
			"callee should receive exactly one traceparent, not the forwarded value plus Dapr's appended one (#10563)")
	case <-time.After(10 * time.Second):
		assert.Fail(t, "timed out waiting for metadata")
	}
}
