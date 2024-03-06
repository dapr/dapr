/*
Copyright 2023 The Dapr Authors
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
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	procdaprd "github.com/dapr/dapr/tests/integration/framework/process/daprd"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	testpb "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation/grpc/proto"
)

func init() {
	suite.Register(new(slowappstartup))
}

// slowappstartup is a test to ensure that service invocation does not error if
// the app is slow to startup.
type slowappstartup struct {
	daprd *procdaprd.Daprd
	app   *app.App
}

func (s *slowappstartup) Setup(t *testing.T) []framework.Option {
	onInvoke := func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
		assert.Equal(t, "Ping", in.GetMethod())
		resp, err := anypb.New(new(testpb.PingResponse))
		if err != nil {
			return nil, err
		}
		return &commonv1.InvokeResponse{
			Data:        resp,
			ContentType: "application/grpc",
		}, nil
	}

	fp := util.ReservePorts(t, 1)
	fp.Free(t)

	s.app = newGRPCServer(t, onInvoke, procgrpc.WithListener(func() (net.Listener, error) {
		// Simulate a slow startup by not opening the listener until 2 seconds after
		// the process starts. This sleep value must be more than the health probe
		// interval.
		time.Sleep(time.Second * 2)
		return net.Listen("tcp", "localhost:"+strconv.Itoa(fp.Port(t, 0)))
	}))

	s.daprd = procdaprd.New(t,
		procdaprd.WithAppProtocol("grpc"),
		procdaprd.WithAppPort(fp.Port(t, 0)),
		procdaprd.WithAppHealthCheck(true),
		procdaprd.WithAppHealthProbeInterval(1),
		procdaprd.WithAppHealthProbeThreshold(1),
	)

	return []framework.Option{
		framework.WithProcesses(s.app, s.daprd),
	}
}

func (s *slowappstartup) Run(t *testing.T, ctx context.Context) {
	s.daprd.WaitUntilRunning(t, ctx)
	s.daprd.WaitUntilAppHealth(t, ctx)

	conn, err := grpc.DialContext(ctx, s.daprd.GRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	require.NoError(t, err)
	client := rtv1.NewDaprClient(conn)

	req, err := anypb.New(new(testpb.PingRequest))
	require.NoError(t, err)

	var pingResp testpb.PingResponse
	var resp *commonv1.InvokeResponse
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		resp, err = client.InvokeService(ctx, &rtv1.InvokeServiceRequest{
			Id: s.daprd.AppID(),
			Message: &commonv1.InvokeRequest{
				Method: "Ping",
				Data:   req,
			},
		})
		// This function must only return that the app is not in a healthy state
		// until the app is in a healthy state.
		//nolint:testifylint
		if !assert.NoError(c, err) {
			require.ErrorContains(c, err, "app is not in a healthy state")
		}
	}, time.Second*3, time.Millisecond*100)
	require.NoError(t, resp.GetData().UnmarshalTo(&pingResp))
}
