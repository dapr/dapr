/*
Copyright 2024 The Dapr Authors
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
	"github.com/dapr/dapr/tests/integration/suite"
	testpb "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation/grpc/proto"
)

func init() {
	suite.Register(new(remotereceiverhastoken))
}

type remotereceiverhastoken struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
	ch     chan metadata.MD
}

func (r *remotereceiverhastoken) Setup(t *testing.T) []framework.Option {
	fn, ch := newServer()
	r.ch = ch
	app := app.New(t,
		app.WithRegister(fn),
		app.WithOnInvokeFn(func(ctx context.Context, _ *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
			md, ok := metadata.FromIncomingContext(ctx)
			require.True(t, ok)
			r.ch <- md
			return new(commonv1.InvokeResponse), nil
		}),
	)

	r.daprd1 = daprd.New(t)
	r.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppAPIToken(t, "abc"),
		daprd.WithAppPort(app.Port(t)),
	)

	return []framework.Option{
		framework.WithProcesses(app, r.daprd1, r.daprd2),
	}
}

func (r *remotereceiverhastoken) Run(t *testing.T, ctx context.Context) {
	r.daprd1.WaitUntilRunning(t, ctx)
	r.daprd2.WaitUntilRunning(t, ctx)

	client := testpb.NewTestServiceClient(r.daprd1.GRPCConn(t, ctx))
	ctx = metadata.AppendToOutgoingContext(ctx, "dapr-app-id", r.daprd2.AppID())
	_, err := client.Ping(ctx, new(testpb.PingRequest))
	require.NoError(t, err)

	select {
	case md := <-r.ch:
		require.Equal(t, []string{"abc"}, md.Get("dapr-api-token"))
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for metadata")
	}

	dclient := r.daprd1.GRPCClient(t, ctx)
	_, err = dclient.InvokeService(ctx, &runtimev1.InvokeServiceRequest{
		Id: r.daprd2.AppID(),
		Message: &commonv1.InvokeRequest{
			Method:        "helloworld",
			Data:          new(anypb.Any),
			HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
		},
	})
	require.NoError(t, err)

	select {
	case md := <-r.ch:
		require.Equal(t, []string{"abc"}, md.Get("dapr-api-token"))
	case <-time.After(5 * time.Second):
		assert.Fail(t, "timed out waiting for metadata")
	}
}
