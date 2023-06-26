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
	subs        []*rtv1.TopicSubscription
	onTopicFunc func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error)
}

func newGRPCServer(t *testing.T, subs []*rtv1.TopicSubscription, onTopicEvent func(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error)) *procgrpc.GRPC {
	return procgrpc.New(t, procgrpc.WithRegister(func(s *grpc.Server) {
		srv := &gServer{
			onTopicFunc: onTopicEvent,
			subs:        subs,
		}
		rtv1.RegisterAppCallbackServer(s, srv)
		rtv1.RegisterAppCallbackHealthCheckServer(s, srv)
	}))
}

func (g *gServer) OnInvoke(context.Context, *commonv1.InvokeRequest) (*commonv1.InvokeResponse, error) {
	return nil, nil
}

func (g *gServer) ListInputBindings(context.Context, *emptypb.Empty) (*rtv1.ListInputBindingsResponse, error) {
	return nil, nil
}

func (g *gServer) ListTopicSubscriptions(context.Context, *emptypb.Empty) (*rtv1.ListTopicSubscriptionsResponse, error) {
	return &rtv1.ListTopicSubscriptionsResponse{
		Subscriptions: g.subs,
	}, nil
}

func (g *gServer) OnBindingEvent(context.Context, *rtv1.BindingEventRequest) (*rtv1.BindingEventResponse, error) {
	return nil, nil
}

func (g *gServer) OnTopicEvent(ctx context.Context, in *rtv1.TopicEventRequest) (*rtv1.TopicEventResponse, error) {
	return g.onTopicFunc(ctx, in)
}

func (g *gServer) HealthCheck(context.Context, *emptypb.Empty) (*rtv1.HealthCheckResponse, error) {
	return &rtv1.HealthCheckResponse{}, nil
}
