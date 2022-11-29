package main

import (
	"context"

	commonv1pb "github.com/dapr/dapr/pkg/proto/common/v1"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// ListInputBindings implements runtime.AppCallbackServer
func (*Server) ListInputBindings(context.Context, *emptypb.Empty) (*runtimev1pb.ListInputBindingsResponse, error) {
	panic("unimplemented")
}

// OnBindingEvent implements runtime.AppCallbackServer
func (*Server) OnBindingEvent(context.Context, *runtimev1pb.BindingEventRequest) (*runtimev1pb.BindingEventResponse, error) {
	panic("unimplemented")
}

// OnInvoke implements runtime.AppCallbackServer
func (*Server) OnInvoke(context.Context, *commonv1pb.InvokeRequest) (*commonv1pb.InvokeResponse, error) {
	panic("unimplemented")
}
