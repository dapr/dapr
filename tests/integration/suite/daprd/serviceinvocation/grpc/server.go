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
	"google.golang.org/protobuf/types/known/emptypb"

	commonv1 "github.com/dapr/dapr/pkg/proto/common/v1"
	rtv1 "github.com/dapr/dapr/pkg/proto/runtime/v1"
	procgrpc "github.com/dapr/dapr/tests/integration/framework/process/grpc"
)

type gServer struct {
	onInvokeFunc func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error)
}

func newGRPCServer(t *testing.T, onInvoke func(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error)) *procgrpc.GRPC {
	return procgrpc.New(t, procgrpc.WithRegister(func(s *grpc.Server) {
		srv := &gServer{
			onInvokeFunc: onInvoke,
		}
		rtv1.RegisterAppCallbackServer(s, srv)
		rtv1.RegisterAppCallbackHealthCheckServer(s, srv)
	}))
}

func (g *gServer) OnInvoke(ctx context.Context, in *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
	return g.onInvokeFunc(ctx, in)
}

func (g *gServer) ListInputBindings(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
	return &rtv1.ListInputBindingsResponse{
		Bindings: []string{"foobar"},
	}, nil
}

func (g *gServer) ListTopicSubscriptions(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
	return nil, nil
}

func (g *gServer) OnBindingEvent(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
	return nil, nil
}

func (g *gServer) OnTopicEvent(context.Context, *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
	return nil, nil
}

func (g *gServer) HealthCheck(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
	return &rtv1.HealthCheckResponse{}, nil
}
