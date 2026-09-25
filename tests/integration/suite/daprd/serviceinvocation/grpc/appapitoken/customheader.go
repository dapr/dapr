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

package appapitoken

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/framework/process/grpc/app/proto"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(customheader))
}

type customheader struct {
	withToken *daprd.Daprd
	noToken   *daprd.Daprd
	ch        chan metadata.MD
}

func (c *customheader) Setup(t *testing.T) []framework.Option {
	c.ch = make(chan metadata.MD, 4)
	app := app.New(t,
		app.WithOnInvokeFn(func(ctx context.Context, _ *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			require.True(t, ok)
			c.ch <- md
			return new(commonv1.InvokeResponse), nil
		}),
		app.WithPingFn(func(ctx context.Context, _ *testpb.PingRequest) (*testpb.PingResponse, error) {
			md, _ := metadata.FromIncomingContext(ctx)
			c.ch <- md
			return new(testpb.PingResponse), nil
		}),
	)

	c.withToken = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "abc"),
		daprd.WithAppAPITokenHeader(t, " X-API-Key "),
		daprd.WithAppPort(app.Port(t)),
	)
	c.noToken = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPITokenHeader(t, " X-API-Key "),
		daprd.WithAppPort(app.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(app, c.withToken, c.noToken),
	}
}

func (c *customheader) Run(t *testing.T, ctx context.Context) {
	c.withToken.WaitUntilRunning(t, ctx)
	c.noToken.WaitUntilRunning(t, ctx)

	exercise := func(daprdProcess *daprd.Daprd, expectedToken string) {
		t.Helper()

		requestCtx := metadata.AppendToOutgoingContext(ctx,
			"dapr-app-id", daprdProcess.AppID(),
			"dapr-api-token", "default-oldtoken",
			"x-api-key", "custom-oldtoken",
		)

		client := testpb.NewTestServiceClient(daprdProcess.GRPCConn(t, ctx))
		_, err := client.Ping(requestCtx, new(testpb.PingRequest))
		require.NoError(t, err)
		c.assertMetadata(t, expectedToken)

		_, err = daprdProcess.GRPCClient(t, ctx).InvokeService(requestCtx, &runtimev1.InvokeServiceRequest{
			Id: daprdProcess.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "helloworld",
				Data:          new(anypb.Any),
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		require.NoError(t, err)
		c.assertMetadata(t, expectedToken)
	}

	exercise(c.withToken, "abc")
	exercise(c.noToken, "")
}

func (c *customheader) assertMetadata(t *testing.T, expectedToken string) {
	t.Helper()
	select {
	case md := <-c.ch:
		assert.Empty(t, md.Get("dapr-api-token"))
		if expectedToken == "" {
			assert.Empty(t, md.Get("x-api-key"))
		} else {
			assert.Equal(t, []string{expectedToken}, md.Get("x-api-key"))
		}
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for custom token metadata")
	}
}
