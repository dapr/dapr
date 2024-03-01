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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/anypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/daprd"
	"github.com/dapr/dapr/tests/integration/framework/process/exec"
	"github.com/dapr/dapr/tests/integration/suite"
	testpb "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation/grpc/proto"
)

func init() {
	suite.Register(new(skew))
}

type skew struct {
	daprd1 *daprd.Daprd
	daprd2 *daprd.Daprd
}

func (s *skew) Setup(t *testing.T) []framework.Option {
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

	srv1 := newGRPCServer(t, onInvoke)
	srv2 := newGRPCServer(t, onInvoke)
	s.daprd1 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv1.Port(t)),
	)
	s.daprd2 = daprd.New(t,
		daprd.WithAppProtocol("grpc"),
		daprd.WithAppPort(srv2.Port(t)),
		daprd.WithExecOptions(exec.WithVersion(t, "1.13")),
	)

	return []framework.Option{
		framework.WithProcesses(srv1, srv2, s.daprd1, s.daprd2),
	}
}

func (s *skew) Run(t *testing.T, ctx context.Context) {
	s.daprd1.WaitUntilRunning(t, ctx)
	s.daprd2.WaitUntilRunning(t, ctx)

	client1 := s.daprd1.GRPCClient(t, ctx)
	client2 := s.daprd1.GRPCClient(t, ctx)

	req, err := anypb.New(new(testpb.PingRequest))
	require.NoError(t, err)

	resp, err := client1.InvokeService(ctx, &rtv1.InvokeServiceRequest{
		Id: s.daprd2.AppID(),
		Message: &commonv1.InvokeRequest{
			Method: "Ping",
			Data:   req,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "application/grpc", resp.GetContentType())

	resp, err = client2.InvokeService(ctx, &rtv1.InvokeServiceRequest{
		Id: s.daprd1.AppID(),
		Message: &commonv1.InvokeRequest{
			Method: "Ping",
			Data:   req,
		},
	})
	require.NoError(t, err)
	assert.Equal(t, "application/grpc", resp.GetContentType())
}
