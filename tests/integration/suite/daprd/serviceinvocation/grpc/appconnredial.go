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
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/listener"
	"github.com/dapr/dapr/tests/integration/framework/log"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/process/ports"
	"github.com/dapr/dapr/tests/integration/suite"
)

func init() {
	suite.Register(new(appconnredial))
}

type appconnredial struct {
	daprd    *daprd.Daprd
	listener *listener.Stall
	logs     *log.Log
}

func (a *appconnredial) Setup(t *testing.T) []framework.Option {
	a.logs = log.New()
	a.listener = listener.New(ports.Reserve(t, 1).Listener(t))

	srv := app.New(t,
		app.WithGRPCOptions(procgrpc.WithListener(func() (net.Listener, error) {
			return a.listener, nil
		})),
		app.WithOnInvokeFn(func(_ context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
			return &commonv1.InvokeResponse{
				Data:        &anypb.Any{Value: []byte("pong")},
				ContentType: "text/plain",
			}, nil
		}),
	)

	a.daprd = daprd.New(t,
		daprd.WithAppPort(srv.Port(t)),
		daprd.WithAppProtocol("grpc"),
		daprd.WithExecOptions(exec.WithStdout(a.logs), exec.WithStderr(a.logs)),
	)

	return []framework.Option{
		framework.WithProcesses(srv, a.daprd),
	}
}

func (a *appconnredial) Run(t *testing.T, ctx context.Context) {
	a.daprd.WaitUntilRunning(t, ctx)

	client := a.daprd.GRPCClient(t, ctx)

	invoke := func() (string, error) {
		resp, err := client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: a.daprd.AppID(),
			Message: &commonv1.InvokeRequest{
				Method:        "ping",
				HttpExtension: &commonv1.HTTPExtension{Verb: commonv1.HTTPExtension_GET},
			},
		})
		if err != nil {
			return "", err
		}
		return string(resp.GetData().GetValue()), nil
	}

	data, err := invoke()
	require.NoError(t, err)
	require.Equal(t, "pong", data)

	a.listener.SetStall(time.Second * 2)
	a.listener.CloseAccepted()

	var (
		lastErr error
		invoked bool
	)
	for start := time.Now(); time.Since(start) < time.Second*30; {
		var data string
		data, lastErr = invoke()
		if lastErr == nil {
			assert.Equal(t, "pong", data)
			invoked = true
			break
		}

		require.NotContains(t, lastErr.Error(), "error reading server preface",
			"app connection was closed mid handshake")

		time.Sleep(time.Millisecond * 100)
	}

	require.True(t, invoked, "app never became reachable again: %v", lastErr)
}
