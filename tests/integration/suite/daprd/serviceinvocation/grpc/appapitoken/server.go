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

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	testpb "github.com/dapr/dapr/tests/integration/suite/daprd/serviceinvocation/grpc/proto"
)

type pingServer struct {
	testpb.UnsafeTestServiceServer
	ch chan metadata.MD
}

func newServer() (func(*grpc.Server), chan metadata.MD) {
	ch := make(chan metadata.MD, 1)
	return func(s *grpc.Server) {
		testpb.RegisterTestServiceServer(s, &pingServer{
			ch: ch,
		})
	}, ch
}

func (p *pingServer) Ping(ctx context.Context, _ *testpb.PingRequest) (*testpb.PingResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	p.ch <- md
	return new(testpb.PingResponse), nil
}
