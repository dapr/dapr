/*
Copyright 2025 The Dapr Authors
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
	suite.Register(new(basicproxy))
}

type basicproxy struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	ch     chan metadata.MD
}

func (b *basicproxy) Setup(t *testing.T) []framework.Option {
	b.ch = make(chan metadata.MD, 1)

	app := app.New(t,
		app.WithPingFn(func(ctx context.Context, _ *testpb.PingRequest) (*testpb.PingResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			b.ch <- md
			return new(testpb.PingResponse), nil
		}),
	)

	b.daprd1 = daprd.New(t,
		daprd.WithAppID("app1"),
		daprd.WithAppProtocol("grpc"),
	)

	b.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(app.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(app, b.daprd1, b.daprd2),
	}
}

func (b *basicproxy) Run(t *testing.T, ctx context.Context) {
	b.daprd1.WaitUntilRunning(t, ctx)
	b.daprd2.WaitUntilRunning(t, ctx)

	client := testpb.NewTestServiceClient(b.daprd1.GRPCConn(t, ctx))
	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", b.daprd2.AppID())
	_, err := client.Ping(ctx, new(testpb.PingRequest))
	require.NoError(t, err)

	select {
	case md := <-b.ch:
		require.Empty(t, md.Get("dapr-app-id"))
		require.Equal(t, md.Get("dapr-callee-app-id"), b.daprd2.AppID())
		require.Equal(t, md.Get("dapr-caller-app-id"), b.daprd1.AppID())
	case <-time.After(10 * time.Second):
		assert.Fail(t, "timed out waiting for metadata")
	}
}
