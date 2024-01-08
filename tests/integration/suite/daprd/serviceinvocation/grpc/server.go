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

	"google.golang.org/grpc"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
	"github.com/dapr/dapr/tests/integration/framework/process/grpc/app"
	testpb "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation/grpc/proto"
)

type (
	invokeFn func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error)
)

type pingserver struct{}

func newGRPCServer(t *testing.T, onInvoke invokeFn, opts ...procgrpc.Option) *app.App {
	return app.New(t,
		app.WithGRPCOptions(opts...),
		app.WithOnInvokeFn(onInvoke),
		app.WithRegister(func(s *grpc.Server) {
			testpb.RegisterTestServiceServer(s, new(pingserver))
		}),
	)
}

func (p *pingserver) Ping(context.Context, *testpb.PingRequest) (*testpb.PingResponse, error) {
	return new(testpb.PingResponse), nil
}
